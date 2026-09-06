package services

import (
	"context"
	"errors"
	"reflect"

	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/framework/jobs"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/framework/observability"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"gorm.io/gorm"
)

var ErrInvalidRuntime = errors.New(
	"services: invalid runtime dependencies",
)

// UnitOfWork is the atomic business-write and audit boundary exposed to
// application modules.
type UnitOfWork interface {
	Run(
		context.Context,
		func(*gorm.DB) (frameworkaudit.Event, error),
	) error
}

// Runtime is a read-only snapshot of provider-neutral dependencies. Database
// is a query handle: its GORM mutation and raw-SQL callbacks fail closed so
// modules cannot accidentally bypass Writes and its atomic audit record.
// Jobs is nil when durable jobs are disabled. Observability lets application
// modules publish business-specific telemetry through the same
// deployment-owned sink as framework instrumentation.
type Runtime struct {
	Database      *module.QueryDatabase
	Writes        UnitOfWork
	Storage       frameworkstorage.ObjectStore
	Jobs          jobs.TransactionalQueue
	Observability *observability.Recorder
	database      *gorm.DB
}

type runtimeContextKey struct{}

// NewRuntime validates dependencies shared by API and worker processes.
func NewRuntime(
	database *gorm.DB,
	writes UnitOfWork,
	objectStore frameworkstorage.ObjectStore,
	jobQueue jobs.TransactionalQueue,
	recorder *observability.Recorder,
) (Runtime, error) {
	if database == nil ||
		nilInterface(objectStore) ||
		(writes != nil && nilInterface(writes)) ||
		(jobQueue != nil && nilInterface(jobQueue)) ||
		recorder == nil {
		return Runtime{}, ErrInvalidRuntime
	}
	queries, err := module.NewQueryDatabase(database)
	if err != nil {
		return Runtime{}, errors.Join(ErrInvalidRuntime, err)
	}
	return Runtime{
		Database:      queries,
		Writes:        writes,
		Storage:       objectStore,
		Jobs:          jobQueue,
		Observability: recorder,
		database:      database,
	}, nil
}

// ContextWithRuntime attaches a validated dependency snapshot.
func ContextWithRuntime(
	ctx context.Context,
	runtime Runtime,
) (context.Context, error) {
	if ctx == nil ||
		runtime.Database == nil ||
		runtime.database == nil ||
		nilInterface(runtime.Storage) ||
		runtime.Observability == nil {
		return nil, ErrInvalidRuntime
	}
	return context.WithValue(ctx, runtimeContextKey{}, runtime), nil
}

// RuntimeFromContext resolves dependencies for the current execution.
func RuntimeFromContext(
	ctx context.Context,
) (Runtime, bool) {
	if ctx == nil {
		return Runtime{}, false
	}
	runtime, ok := ctx.Value(runtimeContextKey{}).(Runtime)
	if !ok ||
		runtime.Database == nil ||
		runtime.database == nil ||
		nilInterface(runtime.Storage) ||
		runtime.Observability == nil {
		return Runtime{}, false
	}
	queries, err := module.NewQueryDatabase(
		runtime.database.WithContext(ctx),
	)
	if err != nil {
		return Runtime{}, false
	}
	runtime.Database = queries
	return runtime, true
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
