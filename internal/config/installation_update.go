package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrInstallationRevisionConflict = errors.New("installation configuration revision conflict")
	installationUpdateMutex         sync.Mutex
)

// UpdateInstallationStorage atomically replaces only the storage profile
// section of an existing installation. Database and session material are
// deliberately copied from the current document and cannot be supplied by an
// HTTP caller.
func UpdateInstallationStorage(
	path string,
	expectedRevision uint64,
	profiles StorageProfileSet,
) (Installation, error) {
	if err := ValidateStorageProfileSet(profiles); err != nil {
		return Installation{}, err
	}
	return updateInstallation(path, expectedRevision, func(next *Installation) error {
		next.ActiveProfileID = profiles.ActiveProfileID
		next.Profiles = cloneStorageProfiles(profiles.Profiles)
		return nil
	})
}

// CommitInstallationStorage keeps the revision lock until the caller's
// mandatory side effect (normally the database audit transaction) succeeds.
// If that callback fails, the exact prior installation document is restored
// before another process can acquire the lock and publish a later revision.
func CommitInstallationStorage(
	path string,
	expectedRevision uint64,
	profiles StorageProfileSet,
	commit func(Installation) error,
) (Installation, error) {
	if commit == nil {
		return Installation{}, errors.New("installation storage commit callback is required")
	}
	if err := ValidateStorageProfileSet(profiles); err != nil {
		return Installation{}, err
	}
	return commitInstallationUpdate(
		path,
		expectedRevision,
		func(candidate *Installation) error {
			candidate.ActiveProfileID = profiles.ActiveProfileID
			candidate.Profiles = cloneStorageProfiles(profiles.Profiles)
			return nil
		},
		commit,
	)
}

// UpdateInstallationFileUploadPolicy atomically replaces only the global file
// upload policy and preserves storage, database, and session configuration.
func UpdateInstallationFileUploadPolicy(
	path string,
	expectedRevision uint64,
	policy FileUploadPolicy,
) (Installation, error) {
	if err := ValidateFileUploadPolicy(policy); err != nil {
		return Installation{}, err
	}
	return updateInstallation(path, expectedRevision, func(next *Installation) error {
		next.FileUploadPolicy = policy
		return nil
	})
}

// CommitInstallationFileUploadPolicy holds the shared installation revision
// lock through the caller's mandatory audit write. A failed callback restores
// the exact prior document before another settings writer can proceed.
func CommitInstallationFileUploadPolicy(
	path string,
	expectedRevision uint64,
	policy FileUploadPolicy,
	commit func(Installation) error,
) (Installation, error) {
	if commit == nil {
		return Installation{}, errors.New("installation file upload policy commit callback is required")
	}
	if err := ValidateFileUploadPolicy(policy); err != nil {
		return Installation{}, err
	}
	return commitInstallationUpdate(
		path,
		expectedRevision,
		func(candidate *Installation) error {
			candidate.FileUploadPolicy = policy
			return nil
		},
		commit,
	)
}

func commitInstallationUpdate(
	path string,
	expectedRevision uint64,
	mutate func(*Installation) error,
	commit func(Installation) error,
) (Installation, error) {
	if expectedRevision == 0 {
		return Installation{}, fmt.Errorf("expected installation revision is required")
	}
	path = filepath.Clean(path)
	installationUpdateMutex.Lock()
	defer installationUpdateMutex.Unlock()

	unlock, err := lockInstallationUpdate(path)
	if err != nil {
		return Installation{}, err
	}
	defer unlock()

	current, next, err := prepareInstallationUpdate(
		path,
		expectedRevision,
		mutate,
	)
	if err != nil {
		return Installation{}, err
	}
	if err := replaceInstallationFile(path, next); err != nil {
		return Installation{}, err
	}
	if err := commit(next); err != nil {
		if restoreErr := replaceInstallationFile(path, current); restoreErr != nil {
			return Installation{}, errors.Join(
				err,
				fmt.Errorf("restore installation configuration: %w", restoreErr),
			)
		}
		return Installation{}, err
	}
	return next, nil
}

// RestoreInstallation replaces a just-written revision with the exact prior
// document when the mandatory audit transaction fails. The caller must prove
// that no later writer won by supplying the current revision.
func RestoreInstallation(path string, expectedCurrentRevision uint64, previous Installation) error {
	path = filepath.Clean(path)
	installationUpdateMutex.Lock()
	defer installationUpdateMutex.Unlock()
	unlock, err := lockInstallationUpdate(path)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := ReadInstallation(path)
	if err != nil {
		return err
	}
	if current.Revision != expectedCurrentRevision {
		return ErrInstallationRevisionConflict
	}
	if err := ValidateInstallation(previous); err != nil {
		return err
	}
	return replaceInstallationFile(path, previous)
}

func updateInstallation(
	path string,
	expectedRevision uint64,
	mutate func(*Installation) error,
) (Installation, error) {
	if expectedRevision == 0 {
		return Installation{}, fmt.Errorf("expected installation revision is required")
	}
	path = filepath.Clean(path)
	installationUpdateMutex.Lock()
	defer installationUpdateMutex.Unlock()

	unlock, err := lockInstallationUpdate(path)
	if err != nil {
		return Installation{}, err
	}
	defer unlock()

	_, next, err := prepareInstallationUpdate(path, expectedRevision, mutate)
	if err != nil {
		return Installation{}, err
	}
	if err := replaceInstallationFile(path, next); err != nil {
		return Installation{}, err
	}
	return next, nil
}

func prepareInstallationUpdate(
	path string,
	expectedRevision uint64,
	mutate func(*Installation) error,
) (Installation, Installation, error) {
	current, err := ReadInstallation(path)
	if err != nil {
		return Installation{}, Installation{}, err
	}
	currentRevision := current.Revision
	if currentRevision != expectedRevision {
		return Installation{}, Installation{}, ErrInstallationRevisionConflict
	}

	next := current
	if err := mutate(&next); err != nil {
		return Installation{}, Installation{}, err
	}
	next.Version = CurrentInstallationVersion
	next.Revision = currentRevision + 1
	next.UpdatedAt = time.Now().UTC()
	if err := ValidateInstallation(next); err != nil {
		return Installation{}, Installation{}, err
	}
	if next.Database != current.Database || next.SessionSecret != current.SessionSecret ||
		!next.InstalledAt.Equal(current.InstalledAt) {
		return Installation{}, Installation{}, errors.New("installation update attempted to modify immutable fields")
	}
	return current, next, nil
}

func replaceInstallationFile(path string, installation Installation) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect installation configuration: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("installation configuration must be a regular non-symbolic file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("installation configuration permissions must not allow group or other access")
	}
	payload, err := encodeInstallation(installation)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errors.New("installation configuration directory must be a real directory")
	}
	temporary, err := os.CreateTemp(directory, ".aginex-config-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create installation update: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure installation update: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write installation update: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync installation update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close installation update: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace installation configuration: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync installation configuration directory: %w", err)
	}
	return nil
}
