package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultConfigFile is the source-tree default. Container images can set
	// AGINEX_CONFIG_FILE=/data/aginex-config.json without changing this value.
	DefaultConfigFile = "data/aginex-config.json"

	// Until Aginex publishes a versioned compatibility boundary, setup reads and
	// writes one strict pre-release installation document identified as v1.
	CurrentInstallationVersion = 1
)

type StateStatus string

const (
	StatusSetup             StateStatus = "setup"
	StatusConfigured        StateStatus = "configured"
	StatusInvalidConfigured StateStatus = "invalid-configured"
)

type DatabaseSource string

const (
	DatabaseSourceManaged     DatabaseSource = "managed"
	DatabaseSourceEnvironment DatabaseSource = "environment"
)

var (
	ErrSetupRequired = errors.New("database setup is required")

	// ErrInstallationSealed marks commit results for which Setup must fail closed:
	// the destination exists, publication may have happened, or its absence
	// cannot be proven reliably.
	ErrInstallationSealed = errors.New("installation configuration is sealed")

	// ErrInstallationExists is both a conflict and a sealed result. It remains a
	// distinct sentinel so callers can preserve the existing conflict response.
	ErrInstallationExists = fmt.Errorf(
		"%w: installation configuration already exists",
		ErrInstallationSealed,
	)
)

// InstallationSealed reports whether an installation commit reached or may
// have raced with the publication boundary, including cases where absence can
// no longer be proven. A sealed error must never reopen Setup.
func InstallationSealed(err error) bool {
	return errors.Is(err, ErrInstallationSealed)
}

type sealedInstallationCommitError struct {
	operation string
	cause     error
	conflict  bool
}

func (err *sealedInstallationCommitError) Error() string {
	return fmt.Sprintf("%s: %v", err.operation, err.cause)
}

func (err *sealedInstallationCommitError) Unwrap() []error {
	causes := []error{ErrInstallationSealed, err.cause}
	if err.conflict {
		causes = append(causes, ErrInstallationExists)
	}
	return causes
}

// Installation is the versioned, durable marker proving setup has completed.
// Managed installations keep their database connection details here. An
// environment marker records only the driver so the DSN remains environment
// owned. Administrator credentials are deliberately not part of this schema.
type Installation struct {
	Version          int                  `json:"version"`
	Revision         uint64               `json:"revision"`
	Database         InstallationDatabase `json:"database"`
	SessionSecret    string               `json:"sessionSecret"`
	InstalledAt      time.Time            `json:"installedAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
	ActiveProfileID  string               `json:"activeProfileId"`
	Profiles         []StorageProfile     `json:"profiles"`
	FileUploadPolicy FileUploadPolicy     `json:"fileUploadPolicy"`
}

type InstallationDatabase struct {
	Source DatabaseSource `json:"source"`
	Driver string         `json:"driver"`
	DSN    string         `json:"dsn,omitempty"`
}

// State is the single startup decision consumed by the API server and worker.
// Invalid configured states always return a non-nil error as well, allowing a
// caller to fail closed without accidentally treating corruption as setup.
type State struct {
	Status                 StateStatus
	Config                 Config
	ConfigFile             string
	Installation           *Installation
	NeedsEnvironmentMarker bool
}

// ConfigFilePath resolves the installation file without inspecting it.
func ConfigFilePath() string {
	env, err := RuntimeEnvironment()
	if err != nil {
		return filepath.Clean(InstallationPath(map[string]string{"AGINEX_CONFIG_FILE": os.Getenv("AGINEX_CONFIG_FILE")}))
	}
	return filepath.Clean(InstallationPath(env))
}

// Load is the configured-only compatibility entry point for callers which must
// reject Setup state. Processes which coordinate Setup or wait for another
// process to finish initialization should call LoadState directly.
func Load() (Config, error) {
	state, err := LoadState()
	if err != nil {
		return Config{}, err
	}
	if state.Status != StatusConfigured {
		return Config{}, ErrSetupRequired
	}
	return state.Config, nil
}

// LoadState resolves environment and durable installation configuration into
// one of three mutually exclusive states: setup, configured, or invalid. Any
// evidence of a previous/attempted installation fails closed.
// LoadState resolves the server dotenv file afresh on every load (including worker polls).
func LoadState() (State, error) {
	env, err := RuntimeEnvironment()
	if err != nil {
		return State{Status: StatusInvalidConfigured}, err
	}
	path := InstallationPath(env)
	installation, present, err := readInstallationIfPresent(path)
	if err != nil {
		return State{Status: StatusInvalidConfigured, ConfigFile: path}, err
	}
	var stored *Installation
	if present {
		stored = &installation
	}
	return ResolveState(env, path, stored)
}

// ResolveState validates a candidate without writing files or changing process environment.
func ResolveState(env map[string]string, path string, stored *Installation) (State, error) {
	state := State{Status: StatusInvalidConfigured, ConfigFile: path}
	cfg, err := environmentValues(env).load()
	if err != nil {
		return state, fmt.Errorf("load runtime configuration: %w", err)
	}
	state.Config = cfg
	environmentDatabase, environmentPresent, err := databaseFromEnvironment(cfg.Database)
	if err != nil {
		return state, err
	}
	installationPresent := stored != nil
	var installation Installation
	if stored != nil {
		installation = *stored
		if err := ValidateInstallation(installation); err != nil {
			return state, err
		}
	}

	if !installationPresent && !environmentPresent {
		cfg.Session.Secret, err = ensureSessionSecret(cfg.Session.Secret)
		if err != nil {
			return state, err
		}
		profile := SynthesizeStorageProfile(cfg.Storage)
		cfg.setStorageRuntime(state.ConfigFile, 1, StorageProfileSet{
			ActiveProfileID: profile.ID,
			Profiles:        []StorageProfile{profile},
		})
		state.Config = cfg
		if err := validateWithoutDatabase(cfg); err != nil {
			return state, err
		}
		state.Status = StatusSetup
		return state, nil
	}

	if installationPresent {
		state.Installation = &installation
		switch installation.Database.Source {
		case DatabaseSourceManaged:
			if environmentPresent {
				return state, fmt.Errorf("managed installation must not also set database environment variables")
			}
			cfg.Database = Database{
				Driver: installation.Database.Driver,
				DSN:    installation.Database.DSN,
			}
		case DatabaseSourceEnvironment:
			if !environmentPresent {
				return state, fmt.Errorf("environment installation requires AGINEX_DATABASE_DRIVER and AGINEX_DATABASE_DSN")
			}
			if installation.Database.Driver != environmentDatabase.Driver {
				return state, fmt.Errorf("AGINEX_DATABASE_DRIVER does not match the installed environment marker")
			}
			cfg.Database = environmentDatabase
		default:
			// readInstallationIfPresent validates this; retain a fail-closed
			// guard in case future construction bypasses the reader.
			return state, fmt.Errorf("unsupported installation database source")
		}
		if strings.TrimSpace(cfg.Session.Secret) == "" {
			cfg.Session.Secret = installation.SessionSecret
		}
		if !cfg.runtime.storageEnvironmentManaged {
			set := StorageProfileSet{
				ActiveProfileID: installation.ActiveProfileID,
				Profiles:        cloneStorageProfiles(installation.Profiles),
			}
			applyEnvironmentLocalRoot(&set, cfg.Storage.LocalRoot)
			if err := validateInstalledStorageEndpoints(cfg.Environment, cfg.Storage.EndpointAllowlist, set); err != nil {
				return state, err
			}
			active, ok := ActiveStorageProfile(set)
			if !ok {
				return state, fmt.Errorf("active storage profile is unavailable")
			}
			endpointAllowlist := append([]string(nil), cfg.Storage.EndpointAllowlist...)
			cfg.Storage = active.StorageConfig()
			cfg.Storage.EndpointAllowlist = endpointAllowlist
			cfg.setStorageRuntime(state.ConfigFile, installation.Revision, set)
		} else {
			environmentProfile := SynthesizeStorageProfile(cfg.Storage)
			set := StorageProfileSet{
				ActiveProfileID: environmentProfile.ID,
				Profiles:        cloneStorageProfiles(installation.Profiles),
			}
			replaced := false
			for index := range set.Profiles {
				if set.Profiles[index].ID == environmentProfile.ID {
					environmentProfile.Name = set.Profiles[index].Name
					environmentProfile.CreatedAt = set.Profiles[index].CreatedAt
					environmentProfile.Used = set.Profiles[index].Used
					set.Profiles[index] = environmentProfile
					replaced = true
					break
				}
			}
			if !replaced {
				set.Profiles = append(set.Profiles, environmentProfile)
			}
			applyEnvironmentLocalRoot(&set, cfg.Storage.LocalRoot)
			if err := validateInstalledStorageEndpoints(cfg.Environment, cfg.Storage.EndpointAllowlist, set); err != nil {
				return state, err
			}
			cfg.setStorageRuntime(state.ConfigFile, installation.Revision, set)
		}
		cfg.runtime.fileUploadPolicy = installation.FileUploadPolicy
	} else {
		cfg.Database = environmentDatabase
		cfg.Session.Secret, err = ensureSessionSecret(cfg.Session.Secret)
		if err != nil {
			return state, err
		}
		marker, markerErr := NewEnvironmentInstallationWithStorage(
			environmentDatabase.Driver,
			cfg.Session.Secret,
			cfg.Storage,
		)
		if markerErr != nil {
			return state, markerErr
		}
		state.Installation = &marker
		state.NeedsEnvironmentMarker = true
		cfg.setStorageRuntime(state.ConfigFile, marker.Revision, StorageProfileSet{
			ActiveProfileID: marker.ActiveProfileID,
			Profiles:        marker.Profiles,
		})
		cfg.runtime.fileUploadPolicy = marker.FileUploadPolicy
	}

	state.Config = cfg
	if err := Validate(cfg); err != nil {
		return state, err
	}
	state.Status = StatusConfigured
	return state, nil
}

func applyEnvironmentLocalRoot(set *StorageProfileSet, localRoot string) {
	if set == nil || strings.TrimSpace(localRoot) == "" {
		return
	}
	for index := range set.Profiles {
		if set.Profiles[index].Provider == StorageProviderLocal {
			set.Profiles[index].LocalRoot = localRoot
		}
	}
}

func validateInstalledStorageEndpoints(environment string, allowlist []string, set StorageProfileSet) error {
	for _, profile := range set.Profiles {
		storage := profile.StorageConfig()
		storage.EndpointAllowlist = append([]string(nil), allowlist...)
		if err := ValidateStorageEndpointPolicy(environment, storage); err != nil {
			return fmt.Errorf("storage profile %s endpoint policy: %w", profile.ID, err)
		}
	}
	return nil
}

func databaseFromEnvironment(database Database) (Database, bool, error) {
	driver := strings.ToLower(strings.TrimSpace(database.Driver))
	dsn := strings.TrimSpace(database.DSN)
	if driver == "" && dsn == "" {
		return Database{}, false, nil
	}
	if driver == "" || dsn == "" {
		return Database{}, false, fmt.Errorf(
			"AGINEX_DATABASE_DRIVER and AGINEX_DATABASE_DSN must either both be set or both be empty",
		)
	}
	return Database{Driver: driver, DSN: dsn}, true, nil
}

// GenerateSessionSecret returns 256 bits encoded without padding so it can be
// stored directly in JSON and used as the server-side session signing secret.
func GenerateSessionSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate session secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ensureSessionSecret(secret string) (string, error) {
	if strings.TrimSpace(secret) != "" {
		return secret, nil
	}
	return GenerateSessionSecret()
}

func NewManagedInstallation(database Database, sessionSecret string) (Installation, error) {
	return NewManagedInstallationWithStorage(database, sessionSecret, Storage{
		Driver:    "local",
		LocalRoot: "data/uploads",
	})
}

func NewManagedInstallationWithStorage(
	database Database,
	sessionSecret string,
	storage Storage,
) (Installation, error) {
	now := time.Now().UTC()
	profile := SynthesizeStorageProfile(storage)
	profile.CreatedAt = now
	profile.UpdatedAt = now
	installation := Installation{
		Version:  CurrentInstallationVersion,
		Revision: 1,
		Database: InstallationDatabase{
			Source: DatabaseSourceManaged,
			Driver: strings.ToLower(strings.TrimSpace(database.Driver)),
			DSN:    strings.TrimSpace(database.DSN),
		},
		SessionSecret:    sessionSecret,
		InstalledAt:      now,
		UpdatedAt:        now,
		ActiveProfileID:  profile.ID,
		Profiles:         []StorageProfile{profile},
		FileUploadPolicy: DefaultFileUploadPolicy(),
	}
	if err := ValidateInstallation(installation); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

func NewEnvironmentInstallation(driver, sessionSecret string) (Installation, error) {
	return NewEnvironmentInstallationWithStorage(driver, sessionSecret, Storage{
		Driver:    "local",
		LocalRoot: "data/uploads",
	})
}

func NewEnvironmentInstallationWithStorage(
	driver, sessionSecret string,
	storage Storage,
) (Installation, error) {
	now := time.Now().UTC()
	profile := SynthesizeStorageProfile(storage)
	profile.CreatedAt = now
	profile.UpdatedAt = now
	installation := Installation{
		Version:  CurrentInstallationVersion,
		Revision: 1,
		Database: InstallationDatabase{
			Source: DatabaseSourceEnvironment,
			Driver: strings.ToLower(strings.TrimSpace(driver)),
		},
		SessionSecret:    sessionSecret,
		InstalledAt:      now,
		UpdatedAt:        now,
		ActiveProfileID:  profile.ID,
		Profiles:         []StorageProfile{profile},
		FileUploadPolicy: DefaultFileUploadPolicy(),
	}
	if err := ValidateInstallation(installation); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

// ValidateInstallation validates the persisted representation without ever
// including a DSN or secret in an error message.
func ValidateInstallation(installation Installation) error {
	if installation.Version != CurrentInstallationVersion {
		return fmt.Errorf("unsupported installation configuration version")
	}
	if installation.InstalledAt.IsZero() {
		return fmt.Errorf("installation timestamp is required")
	}
	if len(installation.SessionSecret) < 32 {
		return fmt.Errorf("installation session secret must contain at least 32 bytes")
	}
	driver := strings.ToLower(strings.TrimSpace(installation.Database.Driver))
	switch driver {
	case "sqlite", "postgres", "mysql":
	default:
		return fmt.Errorf("unsupported installation database driver")
	}
	switch installation.Database.Source {
	case DatabaseSourceManaged:
		if strings.TrimSpace(installation.Database.DSN) == "" {
			return fmt.Errorf("managed installation database DSN is required")
		}
	case DatabaseSourceEnvironment:
		if installation.Database.DSN != "" {
			return fmt.Errorf("environment installation must not persist a database DSN")
		}
	default:
		return fmt.Errorf("unsupported installation database source")
	}
	if installation.Revision == 0 || installation.UpdatedAt.IsZero() {
		return fmt.Errorf("installation revision and update timestamp are required")
	}
	if err := ValidateStorageProfileSet(StorageProfileSet{
		ActiveProfileID: installation.ActiveProfileID,
		Profiles:        installation.Profiles,
	}); err != nil {
		return err
	}
	if err := ValidateFileUploadPolicy(installation.FileUploadPolicy); err != nil {
		return err
	}
	return nil
}

// ReadInstallation reads an existing installation using the same strict file
// and JSON validation applied during startup.
func ReadInstallation(path string) (Installation, error) {
	installation, present, err := readInstallationIfPresent(filepath.Clean(path))
	if err != nil {
		return Installation{}, err
	}
	if !present {
		return Installation{}, os.ErrNotExist
	}
	return installation, nil
}

func readInstallationIfPresent(path string) (Installation, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Installation{}, false, nil
	}
	if err != nil {
		return Installation{}, false, fmt.Errorf("inspect installation configuration: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Installation{}, false, fmt.Errorf("installation configuration must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return Installation{}, false, fmt.Errorf("installation configuration must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Installation{}, false, fmt.Errorf("installation configuration permissions must not allow group or other access")
	}

	file, err := os.Open(path)
	if err != nil {
		return Installation{}, false, fmt.Errorf("open installation configuration: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Installation{}, false, fmt.Errorf("inspect opened installation configuration: %w", err)
	}
	if !os.SameFile(info, openedInfo) {
		return Installation{}, false, fmt.Errorf("installation configuration changed while opening")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(currentInfo, openedInfo) {
		return Installation{}, false, fmt.Errorf("installation configuration changed while opening")
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm()&0o077 != 0 {
		return Installation{}, false, fmt.Errorf("installation configuration permissions changed while opening")
	}
	if openedInfo.Size() > 64<<10 {
		return Installation{}, false, fmt.Errorf("installation configuration exceeds 64 KiB")
	}

	payload, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return Installation{}, false, fmt.Errorf("read installation configuration: %w", err)
	}
	if len(payload) > 64<<10 {
		return Installation{}, false, fmt.Errorf("installation configuration exceeds 64 KiB")
	}
	var installation Installation
	if err := decodeStrictJSON(payload, &installation); err != nil {
		return Installation{}, false, err
	}
	if err := ValidateInstallation(installation); err != nil {
		return Installation{}, false, err
	}
	installation.Database.Driver = strings.ToLower(strings.TrimSpace(installation.Database.Driver))
	return installation, true, nil
}

func decodeStrictJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode installation configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode installation configuration: multiple JSON values")
		}
		return fmt.Errorf("decode installation configuration: %w", err)
	}
	return nil
}

// CommitInstallation durably publishes a new installation file. The temporary
// file is synced before an exclusive rename makes the complete JSON visible in
// one step without replacing an existing target. The containing directory is
// synced after publication. An error for which InstallationSealed returns true
// means the destination exists, may already be published, or cannot reliably
// be proven absent; Setup must remain closed in all three cases.
func CommitInstallation(path string, installation Installation) error {
	return commitInstallation(path, installation, installationCommitOps{
		inspectDestination: os.Lstat,
		makeDirectory:      os.Mkdir,
		publish:            publishInstallation,
		syncDirectory:      syncDirectory,
	})
}

func encodeInstallation(installation Installation) ([]byte, error) {
	payload, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode installation configuration: %w", err)
	}
	return append(payload, '\n'), nil
}

type installationCommitOps struct {
	inspectDestination func(string) (os.FileInfo, error)
	makeDirectory      func(string, os.FileMode) error
	publish            func(string, string) error
	syncDirectory      func(string) error
}

func commitInstallation(path string, installation Installation, ops installationCommitOps) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("installation configuration path is required")
	}
	path = filepath.Clean(path)
	if err := ValidateInstallation(installation); err != nil {
		return err
	}
	installation.Version = CurrentInstallationVersion
	payload, err := encodeInstallation(installation)
	if err != nil {
		return err
	}

	directory := filepath.Dir(path)
	if ops.inspectDestination == nil {
		ops.inspectDestination = os.Lstat
	}
	if ops.makeDirectory == nil {
		ops.makeDirectory = os.Mkdir
	}
	if err := prepareInstallationDirectory(directory, ops); err != nil {
		return classifyPrePublicationFailure(path, err, ops.inspectDestination)
	}
	// Resolve an already-sealed destination before probing directory durability:
	// even if fsync itself is unavailable, an existing marker must never be
	// misclassified as a retryable pre-publication failure.
	if _, err := ops.inspectDestination(path); err == nil {
		return ErrInstallationExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return &sealedInstallationCommitError{
			operation: "cannot verify installation configuration destination is absent",
			cause:     err,
		}
	}
	// Exercise directory fsync before the commit boundary. On failure, recheck
	// the destination: only a confirmed absence is safely retryable because a
	// concurrent installer may have published while this probe was running.
	if err := ops.syncDirectory(directory); err != nil {
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("verify installation configuration directory durability: %w", err),
			ops.inspectDestination,
		)
	}

	temporary, err := os.CreateTemp(directory, ".aginex-config-*.tmp")
	if err != nil {
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("create temporary installation configuration: %w", err),
			ops.inspectDestination,
		)
	}
	temporaryPath := temporary.Name()
	defer func() {
		// Rename removes the source on supported platforms. The fallback
		// publisher uses a hard link, so this also retries best-effort cleanup.
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("secure temporary installation configuration: %w", err),
			ops.inspectDestination,
		)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("write temporary installation configuration: %w", err),
			ops.inspectDestination,
		)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("sync temporary installation configuration: %w", err),
			ops.inspectDestination,
		)
	}
	if err := temporary.Close(); err != nil {
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("close temporary installation configuration: %w", err),
			ops.inspectDestination,
		)
	}

	if err := ops.publish(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrInstallationExists
		}
		return classifyPrePublicationFailure(
			path,
			fmt.Errorf("publish installation configuration: %w", err),
			ops.inspectDestination,
		)
	}
	// The exclusive rename is the commit boundary: from this point forward the
	// complete, already-synced file is visible and startup must switch to the
	// configured application. Preserve any directory-sync failure, but classify
	// it as sealed so the caller cannot confuse it with a retryable Setup error.
	if err := ops.syncDirectory(directory); err != nil {
		return &sealedInstallationCommitError{
			operation: "sync published installation configuration directory",
			cause:     err,
		}
	}
	return nil
}

func classifyPrePublicationFailure(
	path string,
	failure error,
	inspectDestination func(string) (os.FileInfo, error),
) error {
	_, inspectErr := inspectDestination(path)
	if inspectErr == nil {
		return &sealedInstallationCommitError{
			operation: "installation configuration destination appeared after a pre-publication failure",
			cause:     failure,
			conflict:  true,
		}
	}
	if errors.Is(inspectErr, os.ErrNotExist) {
		return failure
	}
	return &sealedInstallationCommitError{
		operation: "cannot verify installation configuration destination after a pre-publication failure",
		cause: errors.Join(
			failure,
			fmt.Errorf("inspect installation configuration destination: %w", inspectErr),
		),
	}
}

// prepareInstallationDirectory creates a missing hierarchy one component at a
// time. Syncing each parent immediately after its child appears is required to
// make every directory entry durable; syncing only the final directory cannot
// persist entries created higher in a multi-level hierarchy.
func prepareInstallationDirectory(directory string, ops installationCommitOps) error {
	missing, err := missingInstallationDirectories(directory)
	if err != nil {
		return err
	}

	for index := len(missing) - 1; index >= 0; index-- {
		child := missing[index]
		if err := ops.makeDirectory(child, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create installation configuration directory: %w", err)
		}

		// EEXIST can be a benign concurrent mkdir, a file, or a symlink. Always
		// inspect the winning object without following it before proceeding.
		info, err := os.Lstat(child)
		if err != nil {
			return fmt.Errorf("inspect created installation configuration directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("installation configuration directory must be a real directory")
		}

		parent := filepath.Dir(child)
		if err := ops.syncDirectory(parent); err != nil {
			return fmt.Errorf("sync installation configuration parent directory: %w", err)
		}
	}

	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect installation configuration directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("installation configuration directory must be a real directory")
	}
	return nil
}

func missingInstallationDirectories(directory string) ([]string, error) {
	current := directory
	missing := make([]string, 0, 4)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				// Preserve compatibility with established absolute paths such as
				// macOS /var while continuing to reject a symlink used as the direct
				// configuration directory.
				if current == directory {
					return nil, fmt.Errorf("installation configuration directory must be a real directory")
				}
				resolved, statErr := os.Stat(current)
				if statErr != nil {
					return nil, fmt.Errorf("inspect installation configuration directory ancestor: %w", statErr)
				}
				if !resolved.IsDir() {
					return nil, fmt.Errorf("installation configuration directory ancestor must be a directory")
				}
			} else if !info.IsDir() {
				return nil, fmt.Errorf("installation configuration directory ancestor must be a directory")
			}
			return missing, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect installation configuration directory: %w", err)
		}

		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("installation configuration directory has no existing ancestor")
		}
		current = parent
	}
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
