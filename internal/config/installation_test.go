package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadStateEntersSetupWithoutDatabaseConfiguration(t *testing.T) {
	path := isolatedInstallationEnvironment(t)

	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusSetup {
		t.Fatalf("status = %q", state.Status)
	}
	if state.ConfigFile != path {
		t.Fatalf("config file = %q", state.ConfigFile)
	}
	if state.Config.Database != (Database{}) {
		t.Fatalf("database = %#v", state.Config.Database)
	}
	if len(state.Config.Session.Secret) < 32 {
		t.Fatalf("generated session secret length = %d", len(state.Config.Session.Secret))
	}
	if state.Installation != nil || state.NeedsEnvironmentMarker {
		t.Fatalf("unexpected installation state = %#v", state)
	}
	if _, err := Load(); !errors.Is(err, ErrSetupRequired) {
		t.Fatalf("configured-only Load error = %v", err)
	}
}

func TestLoadStateCreatesEnvironmentMarkerWithoutDSN(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	t.Setenv("AGINEX_DATABASE_DRIVER", "POSTGRES")
	t.Setenv("AGINEX_DATABASE_DSN", "postgres://secret@example.com/aginex")

	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusConfigured || !state.NeedsEnvironmentMarker {
		t.Fatalf("state = %#v", state)
	}
	if state.Installation == nil ||
		state.Installation.Database.Source != DatabaseSourceEnvironment ||
		state.Installation.Database.Driver != "postgres" ||
		state.Installation.Database.DSN != "" {
		t.Fatalf("environment marker = %#v", state.Installation)
	}
	if err := CommitInstallation(path, *state.Installation); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != StatusConfigured || reloaded.NeedsEnvironmentMarker {
		t.Fatalf("reloaded state = %#v", reloaded)
	}
	if reloaded.Config.Database.DSN != "postgres://secret@example.com/aginex" {
		t.Fatalf("runtime DSN = %q", reloaded.Config.Database.DSN)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "postgres://secret") || strings.Contains(string(raw), `"dsn"`) {
		t.Fatalf("environment marker persisted DSN: %s", raw)
	}
}

func TestManagedInstallationCommitIsExclusiveAndSecure(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "aginex.db")},
		state.Config.Session.Secret,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitInstallation(path, installation); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("installation permissions = %o", got)
	}

	replacement := installation
	replacement.Database.DSN = "must-not-replace.db"
	if err := CommitInstallation(path, replacement); !errors.Is(err, ErrInstallationExists) ||
		!InstallationSealed(err) {
		t.Fatalf("second commit error = %v", err)
	}
	persisted, err := ReadInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Database.DSN != installation.Database.DSN {
		t.Fatalf("existing installation was replaced: %#v", persisted.Database)
	}

	reloaded, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != StatusConfigured ||
		reloaded.Config.Database != (Database{Driver: "sqlite", DSN: installation.Database.DSN}) {
		t.Fatalf("reloaded managed state = %#v", reloaded)
	}
}

func TestCommitInstallationReturnsSealedErrorAfterPublishBoundary(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected directory sync failure")
	syncCalls := 0
	err = commitInstallation(path, installation, installationCommitOps{
		publish: publishInstallation,
		syncDirectory: func(string) error {
			syncCalls++
			if syncCalls == 2 {
				return injected
			}
			return nil
		},
	})
	if err == nil || !InstallationSealed(err) || !errors.Is(err, injected) {
		t.Fatalf("post-publish error = %v", err)
	}
	if _, err := ReadInstallation(path); err != nil {
		t.Fatalf("published installation is not readable: %v", err)
	}
}

func TestCommitInstallationReturnsUnsealedErrorBeforePublishBoundary(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected pre-publish directory sync failure")
	published := false
	inspectCalls := 0
	err = commitInstallation(path, installation, installationCommitOps{
		inspectDestination: func(path string) (os.FileInfo, error) {
			inspectCalls++
			return os.Lstat(path)
		},
		publish: func(string, string) error {
			published = true
			return nil
		},
		syncDirectory: func(string) error {
			return injected
		},
	})
	if err == nil || InstallationSealed(err) || !errors.Is(err, injected) {
		t.Fatalf("pre-publish error = %v", err)
	}
	if published {
		t.Fatal("installation was published after pre-publish durability failure")
	}
	if inspectCalls != 2 {
		t.Fatalf("destination inspections = %d, want 2", inspectCalls)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists after pre-publish failure: %v", err)
	}
}

func TestCommitInstallationSealsDestinationCreatedDuringPreflightFailure(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	installation := testManagedInstallation(t)
	injected := errors.New("injected preflight sync failure")
	published := false

	err := commitInstallation(path, installation, installationCommitOps{
		publish: func(string, string) error {
			published = true
			return nil
		},
		syncDirectory: func(string) error {
			if err := os.WriteFile(path, []byte("created by racing installer\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return injected
		},
	})
	if err == nil || !InstallationSealed(err) ||
		!errors.Is(err, ErrInstallationExists) || !errors.Is(err, injected) {
		t.Fatalf("racing destination error = %v", err)
	}
	if published {
		t.Fatal("publish was called after the preflight failure")
	}
	if raw, readErr := os.ReadFile(path); readErr != nil ||
		string(raw) != "created by racing installer\n" {
		t.Fatalf("racing destination = %q, error = %v", raw, readErr)
	}
}

func TestCommitInstallationSealsAmbiguousDestinationAfterPreflightFailure(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	installation := testManagedInstallation(t)
	injected := errors.New("injected preflight sync failure")
	inspectCalls := 0

	err := commitInstallation(path, installation, installationCommitOps{
		inspectDestination: func(path string) (os.FileInfo, error) {
			inspectCalls++
			if inspectCalls == 1 {
				return os.Lstat(path)
			}
			return nil, &os.PathError{
				Op:   "lstat",
				Path: path,
				Err:  os.ErrPermission,
			}
		},
		publish: publishInstallation,
		syncDirectory: func(string) error {
			return injected
		},
	})
	if err == nil || !InstallationSealed(err) ||
		!errors.Is(err, injected) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("ambiguous destination error = %v", err)
	}
	if inspectCalls != 2 {
		t.Fatalf("destination inspections = %d, want 2", inspectCalls)
	}
}

func TestCommitInstallationSealsAmbiguousInitialDestinationInspection(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	durabilityProbed := false

	err := commitInstallation(path, testManagedInstallation(t), installationCommitOps{
		inspectDestination: func(path string) (os.FileInfo, error) {
			return nil, &os.PathError{
				Op:   "lstat",
				Path: path,
				Err:  os.ErrPermission,
			}
		},
		publish: publishInstallation,
		syncDirectory: func(string) error {
			durabilityProbed = true
			return nil
		},
	})
	if err == nil || !InstallationSealed(err) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("initial ambiguous destination error = %v", err)
	}
	if durabilityProbed {
		t.Fatal("durability was probed without proving the destination absent")
	}
}

func TestCommitInstallationClassifiesPublishConflictAsSealed(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = commitInstallation(path, installation, installationCommitOps{
		publish: func(string, string) error {
			return os.ErrExist
		},
		syncDirectory: func(string) error { return nil },
	})
	if !errors.Is(err, ErrInstallationExists) || !InstallationSealed(err) {
		t.Fatalf("publish conflict error = %v", err)
	}
}

func TestCommitInstallationClassifiesPreexistingDestinationBeforeDurabilityProbe(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("already sealed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	durabilityProbed := false
	err = commitInstallation(path, installation, installationCommitOps{
		publish: func(string, string) error {
			t.Fatal("publish called for an existing destination")
			return nil
		},
		syncDirectory: func(string) error {
			durabilityProbed = true
			return errors.New("must not mask sealed destination")
		},
	})
	if !errors.Is(err, ErrInstallationExists) || !InstallationSealed(err) {
		t.Fatalf("preexisting destination error = %v", err)
	}
	if durabilityProbed {
		t.Fatal("directory durability probe masked the preexisting sealed destination")
	}
}

func TestCommitInstallationPersistsNestedDirectoryEntriesInOrder(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "first", "second", "third")
	path := filepath.Join(directory, "aginex-config.json")
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	relative := func(path string) string {
		relativePath, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			t.Fatal(relativeErr)
		}
		return relativePath
	}
	var events []string
	err = commitInstallation(path, installation, installationCommitOps{
		makeDirectory: func(path string, mode os.FileMode) error {
			events = append(events, "mkdir "+relative(path))
			return os.Mkdir(path, mode)
		},
		publish: func(source, destination string) error {
			events = append(events, "publish")
			return publishInstallation(source, destination)
		},
		syncDirectory: func(path string) error {
			events = append(events, "sync "+relative(path))
			return syncDirectory(path)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"mkdir first",
		"sync .",
		"mkdir " + filepath.Join("first", "second"),
		"sync first",
		"mkdir " + filepath.Join("first", "second", "third"),
		"sync " + filepath.Join("first", "second"),
		"sync " + filepath.Join("first", "second", "third"),
		"publish",
		"sync " + filepath.Join("first", "second", "third"),
	}
	if strings.Join(events, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
}

func TestCommitInstallationStopsOnNestedDirectorySyncFailure(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "first", "second", "third")
	path := filepath.Join(directory, "aginex-config.json")
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected nested parent sync failure")
	failingParent := filepath.Join(root, "first")
	published := false
	err = commitInstallation(path, installation, installationCommitOps{
		makeDirectory: os.Mkdir,
		publish: func(string, string) error {
			published = true
			return nil
		},
		syncDirectory: func(path string) error {
			if path == failingParent {
				return injected
			}
			return syncDirectory(path)
		},
	})
	if err == nil || InstallationSealed(err) || !errors.Is(err, injected) {
		t.Fatalf("nested directory sync error = %v", err)
	}
	if published {
		t.Fatal("installation was published after a nested directory sync failure")
	}
	if info, statErr := os.Lstat(filepath.Join(root, "first", "second")); statErr != nil || !info.IsDir() {
		t.Fatalf("second directory = %#v, error = %v", info, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "first", "second", "third")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("creation continued after sync failure: %v", statErr)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after sync failure: %v", statErr)
	}
}

func TestCommitInstallationHandlesConcurrentDirectoryCreation(t *testing.T) {
	t.Run("directory wins race", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "concurrent")
		path := filepath.Join(first, "nested", "aginex-config.json")
		installation := testManagedInstallation(t)
		injectedRace := false

		err := commitInstallation(path, installation, installationCommitOps{
			makeDirectory: func(path string, mode os.FileMode) error {
				if path == first && !injectedRace {
					injectedRace = true
					if err := os.Mkdir(path, mode); err != nil {
						t.Fatal(err)
					}
					return os.ErrExist
				}
				return os.Mkdir(path, mode)
			},
			publish:       publishInstallation,
			syncDirectory: syncDirectory,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !injectedRace {
			t.Fatal("concurrent EEXIST race was not injected")
		}
		if _, err := ReadInstallation(path); err != nil {
			t.Fatalf("read installation after benign race: %v", err)
		}
	})

	t.Run("file wins race", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "concurrent")
		path := filepath.Join(first, "nested", "aginex-config.json")
		installation := testManagedInstallation(t)
		published := false

		err := commitInstallation(path, installation, installationCommitOps{
			makeDirectory: func(path string, _ os.FileMode) error {
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				return os.ErrExist
			},
			publish: func(string, string) error {
				published = true
				return nil
			},
			syncDirectory: syncDirectory,
		})
		if err == nil || !InstallationSealed(err) {
			t.Fatalf("file EEXIST error = %v", err)
		}
		if published {
			t.Fatal("installation was published through a file directory conflict")
		}
	})

	t.Run("symlink wins race", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic-link creation may require elevated privileges")
		}
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		first := filepath.Join(root, "concurrent")
		path := filepath.Join(first, "nested", "aginex-config.json")
		installation := testManagedInstallation(t)
		published := false

		err := commitInstallation(path, installation, installationCommitOps{
			makeDirectory: func(path string, _ os.FileMode) error {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				return os.ErrExist
			},
			publish: func(string, string) error {
				published = true
				return nil
			},
			syncDirectory: syncDirectory,
		})
		if err == nil || InstallationSealed(err) {
			t.Fatalf("symlink EEXIST error = %v", err)
		}
		if published {
			t.Fatal("installation was published through a symlink directory conflict")
		}
		if _, statErr := os.Lstat(filepath.Join(target, "nested", "aginex-config.json")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("installation was written through concurrent symlink: %v", statErr)
		}
	})
}

func TestCommitInstallationRejectsSymlinkConfigurationDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation may require elevated privileges")
	}
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkDirectory := filepath.Join(root, "config-link")
	if err := os.Symlink(realDirectory, symlinkDirectory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(symlinkDirectory, "aginex-config.json")
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = CommitInstallation(path, installation)
	if err == nil || InstallationSealed(err) {
		t.Fatalf("symlink directory error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(realDirectory, "aginex-config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installation was written through a symlink: %v", err)
	}
}

func TestCommitInstallationAllowsExistingAncestorSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation may require elevated privileges")
	}
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(root, "system-path-alias")
	if err := os.Symlink(realDirectory, ancestor); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ancestor, "data", "nested", "aginex-config.json")

	if err := CommitInstallation(path, testManagedInstallation(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstallation(path); err != nil {
		t.Fatalf("read installation through existing ancestor symlink: %v", err)
	}
	if _, err := ReadInstallation(
		filepath.Join(realDirectory, "data", "nested", "aginex-config.json"),
	); err != nil {
		t.Fatalf("read installation through resolved path: %v", err)
	}
}

func TestLoadStateFailsClosedForPartialOrMissingEnvironmentDatabase(t *testing.T) {
	t.Run("partial environment without marker", func(t *testing.T) {
		isolatedInstallationEnvironment(t)
		t.Setenv("AGINEX_DATABASE_DRIVER", "postgres")

		state, err := LoadState()
		if err == nil || state.Status != StatusInvalidConfigured {
			t.Fatalf("state = %#v, error = %v", state, err)
		}
	})

	t.Run("environment marker after variables disappear", func(t *testing.T) {
		path := isolatedInstallationEnvironment(t)
		secret, err := GenerateSessionSecret()
		if err != nil {
			t.Fatal(err)
		}
		marker, err := NewEnvironmentInstallation("postgres", secret)
		if err != nil {
			t.Fatal(err)
		}
		if err := CommitInstallation(path, marker); err != nil {
			t.Fatal(err)
		}

		state, err := LoadState()
		if err == nil || state.Status != StatusInvalidConfigured {
			t.Fatalf("state = %#v, error = %v", state, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("error leaked session secret")
		}
	})
}

func TestLoadStateRejectsUnsafeInstallationFiles(t *testing.T) {
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := NewManagedInstallation(Database{Driver: "sqlite", DSN: "aginex.db"}, secret)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
	}{
		{name: "malformed", content: `{`, mode: 0o600},
		{
			name: "unknown field",
			content: `{"version":1,"database":{"source":"managed","driver":"sqlite","dsn":"aginex.db"},` +
				`"sessionSecret":"` + secret + `","installedAt":"` + valid.InstalledAt.Format("2006-01-02T15:04:05.999999999Z07:00") + `","adminPassword":"secret"}`,
			mode: 0o600,
		},
		{
			name: "unsupported version",
			content: `{"version":2,"database":{"source":"managed","driver":"sqlite","dsn":"aginex.db"},` +
				`"sessionSecret":"` + secret + `","installedAt":"` + valid.InstalledAt.Format("2006-01-02T15:04:05.999999999Z07:00") + `"}`,
			mode: 0o600,
		},
		{
			name: "permissions too broad",
			content: `{"version":1,"database":{"source":"managed","driver":"sqlite","dsn":"aginex.db"},` +
				`"sessionSecret":"` + secret + `","installedAt":"` + valid.InstalledAt.Format("2006-01-02T15:04:05.999999999Z07:00") + `"}`,
			mode: 0o644,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := isolatedInstallationEnvironment(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.content), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}

			state, err := LoadState()
			if err == nil || state.Status != StatusInvalidConfigured {
				t.Fatalf("state = %#v, error = %v", state, err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "adminPassword\":\"secret") {
				t.Fatalf("error leaked installation content: %v", err)
			}
		})
	}
}

func TestLoadStateRejectsInstallationSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation may require elevated privileges")
	}
	path := isolatedInstallationEnvironment(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState()
	if err == nil || state.Status != StatusInvalidConfigured {
		t.Fatalf("state = %#v, error = %v", state, err)
	}
}

func TestProductionSetupStillValidatesTransportSecurity(t *testing.T) {
	isolatedInstallationEnvironment(t)
	t.Setenv("AGINEX_ENV", "production")
	t.Setenv("AGINEX_SESSION_SECURE", "true")
	t.Setenv("AGINEX_API_PUBLIC_URL", "https://api.example.com")
	t.Setenv("AGINEX_WEB_ORIGINS", "https://admin.example.com")

	state, err := LoadState()
	if err != nil || state.Status != StatusSetup {
		t.Fatalf("secure production setup state = %#v, error = %v", state, err)
	}

	t.Setenv("AGINEX_SESSION_SECURE", "false")
	state, err = LoadState()
	if err == nil || state.Status != StatusInvalidConfigured {
		t.Fatalf("insecure production state = %#v, error = %v", state, err)
	}
}

func testManagedInstallation(t *testing.T) Installation {
	t.Helper()
	secret, err := GenerateSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		secret,
	)
	if err != nil {
		t.Fatal(err)
	}
	return installation
}

func isolatedInstallationEnvironment(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "aginex-config.json")
	t.Setenv("AGINEX_CONFIG_FILE", path)
	t.Setenv("AGINEX_ENV", "development")
	t.Setenv("AGINEX_DATABASE_DRIVER", "")
	t.Setenv("AGINEX_DATABASE_DSN", "")
	t.Setenv("AGINEX_SESSION_SECRET", "")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
	t.Setenv("AGINEX_SESSION_SECURE", "false")
	t.Setenv("AGINEX_API_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("AGINEX_WEB_ORIGINS", "http://localhost:3000")
	return path
}
