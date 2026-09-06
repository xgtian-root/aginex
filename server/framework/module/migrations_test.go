package module

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRegistryMigrationExecutionIsOrderedAndForwardsInputs(t *testing.T) {
	registry := NewRegistry()
	database := &sql.DB{}
	type contextKey string
	const markerKey contextKey = "marker"
	ctx := context.WithValue(context.Background(), markerKey, "present")
	var calls []string

	newBundle := func(t *testing.T, name string) MigrationBundle {
		t.Helper()
		record := func(action string) MigrationFunc {
			return func(gotContext context.Context, gotDatabase *sql.DB, gotDialect Dialect) error {
				if gotContext.Value(markerKey) != "present" {
					t.Fatalf("%s context was not forwarded", name)
				}
				if gotDatabase != database {
					t.Fatalf("%s database = %p, want %p", name, gotDatabase, database)
				}
				calls = append(calls, name+":"+action+":"+string(gotDialect))
				return nil
			}
		}
		bundle, err := NewExecutableMigrationBundle(
			name,
			MigrationExecutor{
				Up:            record("up"),
				EnsureCurrent: record("ensure"),
			},
			MigrationSource{Dialect: DialectPostgreSQL, Directory: "migrations/postgres"},
		)
		if err != nil {
			t.Fatal(err)
		}
		return bundle
	}

	for _, bundle := range []MigrationBundle{
		newBundle(t, "zeta"),
		newBundle(t, "alpha"),
	} {
		if err := registry.RegisterMigrationBundle(bundle); err != nil {
			t.Fatal(err)
		}
	}

	if err := registry.MigrateUp(ctx, database, DialectPostgreSQL); err != nil {
		t.Fatal(err)
	}
	if err := registry.EnsureMigrationsCurrent(ctx, database, DialectPostgreSQL); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"alpha:up:postgres",
		"zeta:up:postgres",
		"alpha:ensure:postgres",
		"zeta:ensure:postgres",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("migration calls = %v, want %v", calls, want)
	}
}

func TestRegistryMigrationExecutionStopsAndWrapsBundleError(t *testing.T) {
	sentinel := errors.New("migration failed")
	tests := []struct {
		name string
		run  func(*Registry, context.Context, *sql.DB) error
	}{
		{
			name: "up",
			run: func(registry *Registry, ctx context.Context, db *sql.DB) error {
				return registry.MigrateUp(ctx, db, DialectSQLite)
			},
		},
		{
			name: "ensure current",
			run: func(registry *Registry, ctx context.Context, db *sql.DB) error {
				return registry.EnsureMigrationsCurrent(ctx, db, DialectSQLite)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			var calls []string
			for _, name := range []string{"alpha", "beta", "zeta"} {
				name := name
				callback := func(context.Context, *sql.DB, Dialect) error {
					calls = append(calls, name)
					if name == "beta" {
						return sentinel
					}
					return nil
				}
				bundle, err := NewExecutableMigrationBundle(
					name,
					MigrationExecutor{
						Up:            callback,
						EnsureCurrent: callback,
					},
					MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := registry.RegisterMigrationBundle(bundle); err != nil {
					t.Fatal(err)
				}
			}

			err := test.run(registry, context.Background(), &sql.DB{})
			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want wrapped sentinel", err)
			}
			if !strings.Contains(err.Error(), `migration bundle "beta"`) {
				t.Fatalf("error = %v, want failing bundle context", err)
			}
			if want := []string{"alpha", "beta"}; !reflect.DeepEqual(calls, want) {
				t.Fatalf("migration calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestRegistryMigrationExecutionFailsClosedWithoutExecutor(t *testing.T) {
	bundle, err := NewMigrationBundle(
		"postmarks",
		MigrationSource{Dialect: DialectMySQL, Directory: "migrations/mysql"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.RegisterMigrationBundle(bundle); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "up",
			run: func() error {
				return registry.MigrateUp(context.Background(), &sql.DB{}, DialectMySQL)
			},
		},
		{
			name: "ensure current",
			run: func() error {
				return registry.EnsureMigrationsCurrent(context.Background(), &sql.DB{}, DialectMySQL)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), `migration bundle "postmarks"`) ||
				!strings.Contains(err.Error(), "no executable callback") {
				t.Fatalf("error = %v, want bundle and missing executor context", err)
			}
		})
	}
}

func TestRegistryMigrationExecutionPreflightsAllBundles(t *testing.T) {
	callbackCalls := 0
	executable, err := NewExecutableMigrationBundle(
		"alpha",
		MigrationExecutor{
			Up: func(context.Context, *sql.DB, Dialect) error {
				callbackCalls++
				return nil
			},
			EnsureCurrent: func(context.Context, *sql.DB, Dialect) error { return nil },
		},
		MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadataOnly, err := NewMigrationBundle(
		"zeta",
		MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
	)
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	for _, bundle := range []MigrationBundle{executable, metadataOnly} {
		if err := registry.RegisterMigrationBundle(bundle); err != nil {
			t.Fatal(err)
		}
	}
	err = registry.MigrateUp(context.Background(), &sql.DB{}, DialectSQLite)
	if !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), `migration bundle "zeta"`) {
		t.Fatalf("error = %v, want metadata-only bundle context", err)
	}
	if callbackCalls != 0 {
		t.Fatalf("callbacks invoked before execution-plan validation: %d", callbackCalls)
	}
}

func TestRegistryMigrationExecutionRequiresDialectSource(t *testing.T) {
	bundle, err := NewExecutableMigrationBundle(
		"postmarks",
		MigrationExecutor{
			Up:            func(context.Context, *sql.DB, Dialect) error { return nil },
			EnsureCurrent: func(context.Context, *sql.DB, Dialect) error { return nil },
		},
		MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.RegisterMigrationBundle(bundle); err != nil {
		t.Fatal(err)
	}

	err = registry.MigrateUp(context.Background(), &sql.DB{}, DialectPostgreSQL)
	if !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), `migration bundle "postmarks"`) ||
		!strings.Contains(err.Error(), `no source for dialect "postgres"`) {
		t.Fatalf("error = %v, want unsupported bundle dialect context", err)
	}
}

func TestEmptyRegistryMigrationExecutionIsNoOp(t *testing.T) {
	registry := NewRegistry()
	if err := registry.MigrateUp(nil, nil, Dialect("unsupported")); err != nil {
		t.Fatalf("empty registry MigrateUp error = %v, want nil", err)
	}
	if err := registry.EnsureMigrationsCurrent(nil, nil, Dialect("unsupported")); err != nil {
		t.Fatalf("empty registry EnsureMigrationsCurrent error = %v, want nil", err)
	}
}

func TestExecutableMigrationBundleRequiresCompleteExecutor(t *testing.T) {
	callback := func(context.Context, *sql.DB, Dialect) error { return nil }
	tests := []struct {
		name     string
		executor MigrationExecutor
	}{
		{name: "missing up", executor: MigrationExecutor{EnsureCurrent: callback}},
		{name: "missing ensure current", executor: MigrationExecutor{Up: callback}},
		{name: "missing both", executor: MigrationExecutor{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewExecutableMigrationBundle(
				"postmarks",
				test.executor,
				MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
			)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestExecutableMigrationBundleCloneKeepsExecutorIsolated(t *testing.T) {
	callCount := 0
	bundle, err := NewExecutableMigrationBundle(
		"postmarks",
		MigrationExecutor{
			Up: func(context.Context, *sql.DB, Dialect) error {
				callCount++
				return nil
			},
			EnsureCurrent: func(context.Context, *sql.DB, Dialect) error { return nil },
		},
		MigrationSource{Dialect: DialectSQLite, Directory: "migrations/sqlite"},
	)
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	if err := registry.RegisterMigrationBundle(bundle); err != nil {
		t.Fatal(err)
	}
	// RegisterModules clones the complete registry before atomically committing
	// a module, so this also verifies that staged registry clones retain the
	// executable callbacks.
	if err := registry.RegisterModules(testModule{name: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	bundle.sources[0].Directory = "changed/original"
	bundle.executor.Up = nil

	snapshots := registry.MigrationBundles()
	if got, want := snapshots[0].Sources()[0].Directory, "migrations/sqlite"; got != want {
		t.Fatalf("snapshot directory = %q, want %q", got, want)
	}
	if snapshots[0].executor.Up == nil {
		t.Fatal("registered bundle lost executable callback")
	}
	snapshots[0].sources[0].Directory = "changed/snapshot"
	snapshots[0].executor.Up = nil

	if err := registry.MigrateUp(context.Background(), &sql.DB{}, DialectSQLite); err != nil {
		t.Fatal(err)
	}

	clonedRegistry := NewRegistry()
	freshSnapshot := registry.MigrationBundles()[0]
	if err := clonedRegistry.RegisterMigrationBundle(freshSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := clonedRegistry.MigrateUp(context.Background(), &sql.DB{}, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	if callCount != 2 {
		t.Fatalf("up callback calls = %d, want 2", callCount)
	}
}
