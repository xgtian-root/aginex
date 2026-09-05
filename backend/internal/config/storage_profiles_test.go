package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateInstallationStorageUsesRevisionCASAndPreservesUploadPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation.json")
	installation, err := NewManagedInstallation(
		Database{Driver: "sqlite", DSN: "aginex.db"},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	installation.FileUploadPolicy = FileUploadPolicy{
		MaxUploadBytes:          32 << 20,
		ResumableUploadsEnabled: true,
	}
	if err := CommitInstallation(path, installation); err != nil {
		t.Fatal(err)
	}
	local := SynthesizeStorageProfile(Storage{Driver: "local", LocalRoot: "data/files"})
	set := StorageProfileSet{ActiveProfileID: local.ID, Profiles: []StorageProfile{local}}

	updated, err := UpdateInstallationStorage(path, installation.Revision, set)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != CurrentInstallationVersion ||
		updated.Revision != installation.Revision+1 ||
		updated.SessionSecret != installation.SessionSecret ||
		updated.Database != installation.Database ||
		!updated.InstalledAt.Equal(installation.InstalledAt) ||
		updated.FileUploadPolicy != installation.FileUploadPolicy {
		t.Fatalf("updated installation = %#v", updated)
	}
	if _, err := UpdateInstallationStorage(path, installation.Revision, set); !errors.Is(err, ErrInstallationRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("installation mode = %v", info.Mode())
	}
}

func TestConcurrentInstallationStorageCASAllowsOneWinner(t *testing.T) {
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
	set := StorageProfileSet{ActiveProfileID: installation.ActiveProfileID, Profiles: installation.Profiles}
	results := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(2)
	for range 2 {
		go func() {
			start.Done()
			start.Wait()
			_, updateErr := UpdateInstallationStorage(path, installation.Revision, set)
			results <- updateErr
		}()
	}
	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInstallationRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent update error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d", successes, conflicts)
	}
}

func TestCommitInstallationStorageRestoresPreviousDocumentWhenCommitFails(t *testing.T) {
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
	profile := installation.Profiles[0]
	profile.Name = "Renamed local"
	set := StorageProfileSet{ActiveProfileID: profile.ID, Profiles: []StorageProfile{profile}}
	want := errors.New("audit unavailable")
	if _, err := CommitInstallationStorage(path, installation.Revision, set, func(Installation) error {
		return want
	}); !errors.Is(err, want) {
		t.Fatalf("commit error = %v", err)
	}
	restored, err := ReadInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != installation.Revision || restored.Profiles[0].Name != installation.Profiles[0].Name {
		t.Fatalf("restored installation = %#v", restored)
	}
}

func TestValidateStorageEndpointPolicy(t *testing.T) {
	cases := []struct {
		name        string
		environment string
		storage     Storage
		wantError   bool
	}{
		{name: "development private HTTP", environment: "development", storage: Storage{Driver: "s3", Endpoint: "http://127.0.0.1:9000"}},
		{name: "metadata", environment: "development", storage: Storage{Driver: "s3", Endpoint: "http://169.254.169.254"}, wantError: true},
		{name: "Alibaba metadata", environment: "development", storage: Storage{Driver: "s3", Endpoint: "http://100.100.100.200"}, wantError: true},
		{name: "metadata hostname with root label", environment: "development", storage: Storage{Driver: "s3", Endpoint: "http://metadata.google.internal."}, wantError: true},
		{name: "userinfo", environment: "development", storage: Storage{Driver: "s3", Endpoint: "https://user@example.com"}, wantError: true},
		{name: "production HTTP", environment: "production", storage: Storage{Driver: "s3", Endpoint: "http://minio.example.com", EndpointAllowlist: []string{"minio.example.com"}}, wantError: true},
		{name: "production not allowed", environment: "production", storage: Storage{Driver: "s3", Endpoint: "https://minio.example.com"}, wantError: true},
		{name: "production allowed", environment: "production", storage: Storage{Driver: "s3", Endpoint: "https://minio.example.com", EndpointAllowlist: []string{"minio.example.com"}}},
		{name: "official OSS", environment: "production", storage: Storage{Driver: "oss", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com"}},
		{name: "unofficial OSS", environment: "development", storage: Storage{Driver: "oss", Endpoint: "https://oss.example.com"}, wantError: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateStorageEndpointPolicy(test.environment, test.storage)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}
