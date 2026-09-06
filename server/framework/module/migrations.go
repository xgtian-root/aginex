package module

import (
	"context"
	"database/sql"
	"fmt"
)

// MigrationFunc performs one migration action for the selected database
// dialect. Implementations must be safe to invoke during process startup and
// must honor context cancellation.
type MigrationFunc func(context.Context, *sql.DB, Dialect) error

// MigrationExecutor turns a MigrationBundle from metadata into an executable
// migration boundary. Up mutates the schema; EnsureCurrent only verifies it.
//
// Both callbacks are required for an executable bundle. Callback references
// are copied by value and must be treated as immutable and concurrency-safe
// after registration.
type MigrationExecutor struct {
	Up            MigrationFunc
	EnsureCurrent MigrationFunc
}

// NewExecutableMigrationBundle validates and copies migration metadata together
// with the callbacks required by migrate and runtime version checks.
//
// NewMigrationBundle remains available for metadata-only consumers. A
// metadata-only bundle is deliberately rejected if a Registry is asked to
// execute or verify migrations.
func NewExecutableMigrationBundle(
	name string,
	executor MigrationExecutor,
	sources ...MigrationSource,
) (MigrationBundle, error) {
	bundle, err := NewMigrationBundle(name, sources...)
	if err != nil {
		return MigrationBundle{}, err
	}
	if !executor.complete() {
		return MigrationBundle{}, fmt.Errorf(
			"%w: migration bundle %q executor requires Up and EnsureCurrent",
			ErrInvalid,
			bundle.Name(),
		)
	}
	bundle.executor = executor
	return bundle, nil
}

// MigrateUp executes every registered migration bundle in deterministic name
// order. Execution stops on the first failure.
func (r *Registry) MigrateUp(ctx context.Context, db *sql.DB, dialect Dialect) error {
	return r.runMigrations(ctx, db, dialect, migrationActionUp)
}

// EnsureMigrationsCurrent verifies every registered migration bundle in
// deterministic name order without mutating schema state. Verification stops
// on the first failure.
func (r *Registry) EnsureMigrationsCurrent(
	ctx context.Context,
	db *sql.DB,
	dialect Dialect,
) error {
	return r.runMigrations(ctx, db, dialect, migrationActionEnsureCurrent)
}

type migrationAction string

const (
	migrationActionUp            migrationAction = "up"
	migrationActionEnsureCurrent migrationAction = "ensure current"
)

func (r *Registry) runMigrations(
	ctx context.Context,
	db *sql.DB,
	dialect Dialect,
	action migrationAction,
) error {
	bundles := r.MigrationBundles()
	if len(bundles) == 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("%w: migration %s requires a context", ErrInvalid, action)
	}
	if db == nil {
		return fmt.Errorf("%w: migration %s requires a database", ErrInvalid, action)
	}
	if !validDialect(dialect) {
		return fmt.Errorf("%w: migration %s has unsupported dialect %q", ErrInvalid, action, dialect)
	}

	// Validate the complete execution plan before invoking any callback. This
	// prevents a later metadata-only or dialect-incomplete bundle from leaving
	// an earlier bundle partially migrated.
	for _, bundle := range bundles {
		if !bundle.supportsDialect(dialect) {
			return fmt.Errorf(
				"%w: migration bundle %q %s has no source for dialect %q",
				ErrInvalid,
				bundle.Name(),
				action,
				dialect,
			)
		}

		callback := bundle.executor.callback(action)
		if callback == nil {
			return fmt.Errorf(
				"%w: migration bundle %q %s has no executable callback",
				ErrInvalid,
				bundle.Name(),
				action,
			)
		}
	}

	for _, bundle := range bundles {
		callback := bundle.executor.callback(action)
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migration bundle %q %s: %w", bundle.Name(), action, err)
		}
		if err := callback(ctx, db, dialect); err != nil {
			return fmt.Errorf("migration bundle %q %s: %w", bundle.Name(), action, err)
		}
	}
	return nil
}

func (b MigrationBundle) supportsDialect(dialect Dialect) bool {
	for _, source := range b.sources {
		if source.Dialect == dialect {
			return true
		}
	}
	return false
}

func (e MigrationExecutor) empty() bool {
	return e.Up == nil && e.EnsureCurrent == nil
}

func (e MigrationExecutor) complete() bool {
	return e.Up != nil && e.EnsureCurrent != nil
}

func (e MigrationExecutor) callback(action migrationAction) MigrationFunc {
	switch action {
	case migrationActionUp:
		return e.Up
	case migrationActionEnsureCurrent:
		return e.EnsureCurrent
	default:
		return nil
	}
}
