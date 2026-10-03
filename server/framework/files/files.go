// Package files exposes verified file metadata and transactional business
// references without exposing storage keys or provider credentials.
package files

import (
	"context"
	"errors"
	"io"

	"gorm.io/gorm"
)

var (
	ErrInvalidArgument    = errors.New("files: invalid argument")
	ErrNotFound           = errors.New("files: file not found")
	ErrNotReady           = errors.New("files: file is not ready")
	ErrInUse              = errors.New("files: file is referenced by a business resource")
	ErrStorageUnavailable = errors.New("files: original storage is unavailable")
	ErrInvalidTransaction = errors.New("files: an active Writes transaction from the same database is required")
	ErrInvalidState       = errors.New("files: reference state is inconsistent")
)

// MaxReferences bounds both one owner's stored set and each Replace request.
const MaxReferences = 1000

// Info contains verified metadata. OwnerID and Visibility are authorization
// inputs for trusted application modules; this package does not authorize a
// browser request. No storage locator or credential is exposed.
type Info struct {
	ID           string
	OriginalName string
	ContentType  string
	Size         int64
	Width        int
	Height       int
	SHA256       string
	OwnerID      string
	Visibility   string
}

// ReferenceOwner identifies a complete business object's reference set.
// Resource is a lowercase audit-style resource name, at most 120 bytes.
// ResourceID is a nonempty, whitespace-trimmed identifier of at most 160 bytes.
type ReferenceOwner struct {
	Resource   string
	ResourceID string
}

// Service is available as Runtime.Files only when FilesModule is enabled.
// Callers enforce business permissions, ownership, and publication rules.
type Service interface {
	// GetReady is a metadata snapshot, not a reservation against deletion.
	GetReady(context.Context, string) (Info, error)
	// Open reads from the file's original storage profile. The caller closes
	// the returned reader and sets safe HTTP presentation headers if needed.
	Open(context.Context, string) (Info, io.ReadCloser, error)
	// Bind requires an active transaction provided by Runtime.Writes.Run.
	// References must not escape that callback or be used concurrently.
	Bind(*gorm.DB) (References, error)
}

type References interface {
	// Replace atomically replaces the owner's entire set. IDs must be canonical
	// UUIDs. Duplicate IDs are removed and returned metadata is sorted by ID.
	// Empty input unbinds everything without deleting files. The owner lock
	// remains, so concurrent first bindings and clears also serialize.
	//
	// Call from the same Writes.Run transaction that saves or deletes the
	// business object, include the ID change in its audit event, and return any
	// error from that callback. Delete an owner by replacing its set with nil.
	// Business-row locking or optimistic concurrency still belongs to the
	// caller. Multiple owners in one transaction must be processed in a stable
	// application-wide order; propagate database deadlock/serialization errors.
	Replace(context.Context, ReferenceOwner, []string) ([]Info, error)
}
