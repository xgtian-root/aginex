package config

import "fmt"

const (
	// FileUploadSizeStepBytes is the granularity accepted by the installation
	// policy so operators cannot publish arbitrary byte limits.
	FileUploadSizeStepBytes int64 = 1 << 20

	DefaultMaxUploadBytes  int64 = 10 << 20
	MaximumFileUploadBytes int64 = 1 << 30
)

// FileUploadPolicy is the durable, installation-wide upload policy. It is
// independent from the active storage profile so changing storage targets
// cannot silently change accepted file sizes or transfer mode.
type FileUploadPolicy struct {
	MaxUploadBytes          int64 `json:"maxUploadBytes"`
	ResumableUploadsEnabled bool  `json:"resumableUploadsEnabled"`
}

// FileUploadRuntime is the immutable policy snapshot loaded when the process
// starts. InstallationPath and revision let settings code read and update the
// pending installation document through the same CAS boundary as storage.
type FileUploadRuntime struct {
	InstallationPath        string
	LoadedRevision          uint64
	EnvironmentManaged      bool
	MaxUploadBytes          int64
	ResumableUploadsEnabled bool
}

func DefaultFileUploadPolicy() FileUploadPolicy {
	return FileUploadPolicy{MaxUploadBytes: DefaultMaxUploadBytes}
}

func ValidateFileUploadPolicy(policy FileUploadPolicy) error {
	if policy.MaxUploadBytes < FileUploadSizeStepBytes {
		return fmt.Errorf(
			"file upload maximum must be at least %d bytes",
			FileUploadSizeStepBytes,
		)
	}
	if policy.MaxUploadBytes > MaximumFileUploadBytes {
		return fmt.Errorf(
			"file upload maximum must not exceed %d bytes",
			MaximumFileUploadBytes,
		)
	}
	if policy.MaxUploadBytes%FileUploadSizeStepBytes != 0 {
		return fmt.Errorf(
			"file upload maximum must use %d-byte increments",
			FileUploadSizeStepBytes,
		)
	}
	return nil
}

func (cfg Config) FileUploadRuntime() FileUploadRuntime {
	policy := cfg.runtime.fileUploadPolicy
	if policy.MaxUploadBytes == 0 {
		policy = DefaultFileUploadPolicy()
	}
	return FileUploadRuntime{
		InstallationPath:        cfg.runtime.installationPath,
		LoadedRevision:          cfg.runtime.loadedRevision,
		EnvironmentManaged:      cfg.runtime.storageEnvironmentManaged,
		MaxUploadBytes:          policy.MaxUploadBytes,
		ResumableUploadsEnabled: policy.ResumableUploadsEnabled,
	}
}
