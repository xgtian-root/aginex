# Public files and business references

Register `application.FilesModule()` in the shared application definition to
make `services.Runtime.Files` available in API and worker contexts. Without
that module, `Files` is nil and its file/reference tables and cleanup handlers
are not registered. The existing worker still requires its configured durable
job provider. The file service itself does not require a queue to read or bind.

Import `github.com/xgtian-root/aginex/server/framework/files`. Derived modules
never need Aginex's `internal` packages or file-object table definitions.

```go
type Service interface {
    GetReady(context.Context, string) (Info, error)
    Open(context.Context, string) (Info, io.ReadCloser, error)
    Bind(*gorm.DB) (References, error)
}

type References interface {
    Replace(context.Context, ReferenceOwner, []string) ([]Info, error)
}

type ReferenceOwner struct {
    Resource   string
    ResourceID string
}
```

`Info` exposes ID, original display name, verified content type, byte size,
width, height, SHA256, uploader OwnerID, and Visibility. It exposes no storage
key, filesystem path, bucket, profile credentials, or provider SDK type.
OwnerID and Visibility are inputs to application authorization, not an access
grant. Draft and published content receive identical reference protection.

## Saving a business object

Use the transaction supplied by `Runtime.Writes.Run`. The following adapter
is complete Go code; `save` is the application's authorized business mutation
and produces its audit event. Both functions execute in the same transaction.

```go
package media

import (
    "context"
    "errors"

    "github.com/xgtian-root/aginex/server/framework/audit"
    "github.com/xgtian-root/aginex/server/framework/files"
    "github.com/xgtian-root/aginex/server/framework/services"
    "gorm.io/gorm"
)

func SaveWithFiles(
    ctx context.Context,
    owner files.ReferenceOwner,
    ids []string,
    save func(*gorm.DB, []files.Info) (audit.Event, error),
) error {
    runtime, ok := services.RuntimeFromContext(ctx)
    if !ok || runtime.Files == nil || runtime.Writes == nil || save == nil {
        return errors.New("file-enabled write runtime is required")
    }
    return runtime.Writes.Run(ctx, func(tx *gorm.DB) (audit.Event, error) {
        refs, err := runtime.Files.Bind(tx)
        if err != nil {
            return audit.Event{}, err
        }
        infos, err := refs.Replace(ctx, owner, ids)
        if err != nil {
            return audit.Event{}, err
        }
        // Check allowed file ownership/type here before saving. Returning an
        // error rolls back the reference changes as well as the business save.
        // Include the old/new file IDs in the sanitized business audit event.
        return save(tx, infos)
    })
}
```

For example, use `files.ReferenceOwner{Resource: "articles", ResourceID: id}`.
The owner covers the object's complete file set, including cover and gallery;
ordering, purpose, and captions remain application data. Pass all retained IDs
on each save. When deleting the business object, pass nil and delete it in the
same transaction. Unbinding never schedules file deletion.

Enforce the business object's permissions and ownership, and lock its row or
apply optimistic concurrency consistently with other application mutations.
If the application locks business rows, do that before replacing references
throughout the application. The adapter above is for mutations whose own
concurrency policy permits the shown order. A file ID stored only in business
data, without `Replace`, has no reference protection.

`Resource` follows `[a-z][a-z0-9_-]*` and is at most 120 bytes. `ResourceID` is
nonempty, trimmed, contains no control characters, and is at most 160 bytes.
IDs must be canonical UUID strings. A request accepts at most 1,000 IDs,
deduplicates them, and returns metadata sorted by ID. Repeating a replacement
does not duplicate rows. Every supplied file must be ready; pending, invalid,
deleting, delete_failed, deleted, and missing files are rejected.

`Bind` rejects root database handles, another database's transaction, and
transactions outside an active `Writes.Run` callback. Do not retain a bound
References value, use it concurrently, or commit the caller's transaction.
Propagate replacement and business errors from the callback. Audit validation
or persistence failure rolls back the whole write. References also use a
savepoint so a failed replacement cannot leave a partial set behind.

## Concurrency and deletion

The Files migration family owns `file_reference_owners` and `file_references`.
The latter has the unique key `(resource, resource_id, file_id)`, foreign keys,
and an index beginning with `file_id`. Empty owner rows remain to serialize
future first bindings and concurrent clears. Their sorted JSON file-ID set is
maintained atomically with the reference rows, and checked for consistency.
This lock metadata avoids stale MySQL repeatable-read snapshots when deciding
which old and new files must be locked.

Replacement locks the owner and then the union of old/new file rows in ID
order. Reference reads after the file locks use current locking reads.
SQLite uses the framework's immediate write transactions; PostgreSQL and MySQL
use row locks. Multiple owners in one transaction must follow a consistent
application order; callers must propagate deadlock/serialization errors and
retry the entire business write when appropriate.

Deletion checks references while holding the file lock, before changing state
or enqueuing cleanup. A referenced file returns HTTP `409 FILE_IN_USE`.
Workers and expiry cleanup recheck before touching the stored object. A file
that has entered a deletion state cannot acquire new references. There is no
force-delete API. Removing one of several owners does not allow deletion;
all references must be removed first. Storage deletion is an external effect:
if its later audit/database commit fails, the durable task retries safely.

## Reading and authorization

`GetReady` is a metadata snapshot, not a reservation. `Replace` performs the
authoritative ready check under the write lock. `Open` returns metadata and a
stream from the file's original storage profile; always close that stream.
Changing the active profile from A to B does not redirect existing files to B.
Missing, unbound, or unavailable original profiles fail closed. Removing a
file's last reference also removes its deletion protection during later reads.
`AGINEX_STORAGE_LOCAL_ROOT` seeds the initial Local profile. With an explicit
`AGINEX_STORAGE_DRIVER`, it selects the environment-managed profile; changing
that root creates a distinct profile identity. It never rewrites saved profiles'
directories, including when the active profile is cloud storage.

The public Go service trusts the calling module. It does not add anonymous
HTTP reads or apply business publication/privacy policy. An application route
must authorize access, choose safe response headers/disposition, and avoid
exposing private bytes or logging them. For managed uploaded files, use Files
rather than manipulating their keys through the lower-level `Runtime.Storage`.
Direct SQL, raw storage deletion, and provider-side administration are outside
the reference-protection boundary.

Use `errors.Is` with `files.ErrInvalidArgument`, `ErrNotFound`, `ErrNotReady`,
`ErrInUse`, `ErrStorageUnavailable`, `ErrInvalidTransaction`, or
`ErrInvalidState`. The last error indicates inconsistent reference metadata;
fail the write and investigate instead of bypassing protection.

## Baseline and release

This is an unpublished Files baseline change for SQLite, PostgreSQL, and
MySQL. API initialization applies migrations; workers only verify them. The
installation configuration remains strict version 1. Stale local draft schemas
use the explicit recoverable `aginex-cli dev reinitialize` workflow; runtime code
does not adopt or repair them silently.

Local source verification and published dependency verification are distinct.
See the framework's [CLI release requirements](https://github.com/xgtian-root/aginex/blob/main/docs/cli-release.md) before assigning a downloadable
BackendVersion. A release must generate a project with `GOWORK=off`, no local
replace, and a real downloaded backend, then verify build, CAPTCHA/login, and
public file use from an independent module.
