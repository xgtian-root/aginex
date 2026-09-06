package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateFileUploadPolicyBoundsAndGranularity(t *testing.T) {
	tests := []struct {
		name      string
		maxBytes  int64
		wantError bool
	}{
		{name: "minimum", maxBytes: FileUploadSizeStepBytes},
		{name: "default", maxBytes: DefaultMaxUploadBytes},
		{name: "maximum", maxBytes: MaximumFileUploadBytes},
		{name: "zero", maxBytes: 0, wantError: true},
		{name: "below minimum", maxBytes: FileUploadSizeStepBytes - 1, wantError: true},
		{name: "not one MiB step", maxBytes: DefaultMaxUploadBytes + 1, wantError: true},
		{name: "above maximum", maxBytes: MaximumFileUploadBytes + FileUploadSizeStepBytes, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateFileUploadPolicy(FileUploadPolicy{
				MaxUploadBytes:          test.maxBytes,
				ResumableUploadsEnabled: true,
			})
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateFileUploadPolicy() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestFileUploadRuntimeUsesDefaultSnapshot(t *testing.T) {
	runtime := WithDefaults(Config{}).FileUploadRuntime()
	if runtime.MaxUploadBytes != DefaultMaxUploadBytes ||
		runtime.ResumableUploadsEnabled {
		t.Fatalf("default file upload runtime = %#v", runtime)
	}
}

func TestInstallationUsesAndRequiresCurrentFileUploadPolicy(t *testing.T) {
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Version != CurrentInstallationVersion ||
		installation.FileUploadPolicy != DefaultFileUploadPolicy() {
		t.Fatalf("new installation = %#v", installation)
	}
	installation.FileUploadPolicy = FileUploadPolicy{}
	if err := ValidateInstallation(installation); err == nil {
		t.Fatal("ValidateInstallation accepted a missing file upload policy")
	}
}

func TestLoadStateSnapshotsInstallationFileUploadPolicy(t *testing.T) {
	path := isolatedInstallationEnvironment(t)
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "aginex.db")},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	installation.FileUploadPolicy = FileUploadPolicy{
		MaxUploadBytes:          64 << 20,
		ResumableUploadsEnabled: true,
	}
	if err := CommitInstallation(path, installation); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	runtime := state.Config.FileUploadRuntime()
	if runtime.InstallationPath != path ||
		runtime.LoadedRevision != installation.Revision ||
		runtime.EnvironmentManaged ||
		runtime.MaxUploadBytes != 64<<20 ||
		!runtime.ResumableUploadsEnabled {
		t.Fatalf("loaded file upload runtime = %#v", runtime)
	}

	updatedPolicy := FileUploadPolicy{MaxUploadBytes: 128 << 20}
	updated, err := UpdateInstallationFileUploadPolicy(
		path,
		installation.Revision,
		updatedPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != installation.Revision+1 ||
		updated.FileUploadPolicy != updatedPolicy {
		t.Fatalf("updated installation = %#v", updated)
	}
	if state.Config.FileUploadRuntime() != runtime {
		t.Fatal("an installation update mutated an already-loaded runtime snapshot")
	}

	reloaded, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Config.FileUploadRuntime(); got.LoadedRevision != updated.Revision ||
		got.MaxUploadBytes != updatedPolicy.MaxUploadBytes ||
		got.ResumableUploadsEnabled {
		t.Fatalf("reloaded file upload runtime = %#v", got)
	}
}

func TestCommitInstallationFileUploadPolicyRestoresOnCallbackFailure(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "installation.json")
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitInstallation(path, installation); err != nil {
		t.Fatal(err)
	}
	want := errors.New("audit unavailable")
	_, err = CommitInstallationFileUploadPolicy(
		path,
		installation.Revision,
		FileUploadPolicy{
			MaxUploadBytes:          32 << 20,
			ResumableUploadsEnabled: true,
		},
		func(Installation) error { return want },
	)
	if !errors.Is(err, want) {
		t.Fatalf("commit error = %v", err)
	}
	restored, err := ReadInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != installation.Revision ||
		restored.FileUploadPolicy != installation.FileUploadPolicy {
		t.Fatalf("restored installation = %#v", restored)
	}
}
