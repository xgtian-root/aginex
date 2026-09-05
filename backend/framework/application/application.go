package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/framework/observability"
	internalapp "github.com/xgtian-root/aginex/backend/internal/app"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
	internalsetup "github.com/xgtian-root/aginex/backend/internal/setup"
	internalworker "github.com/xgtian-root/aginex/backend/internal/worker"
	"gorm.io/gorm"
)

type (
	// App is the composed Aginex HTTP runtime.
	App = internalapp.App
	// Worker is the composed durable background runtime.
	Worker = internalworker.Runtime

	// Config and its component aliases let a derived Go module configure Aginex
	// without importing implementation-only internal packages.
	Config            = config.Config
	HTTPConfig        = config.HTTP
	DatabaseConfig    = config.Database
	SessionConfig     = config.Session
	RateLimitConfig   = config.RateLimit
	IdempotencyConfig = config.Idempotency
	JobsConfig        = config.Jobs
	BootstrapConfig   = config.Bootstrap
	BootstrapOptions  = internalapp.BootstrapOptions
	StorageConfig     = config.Storage
)

// ErrJobsDisabled is returned when a worker is requested without a durable
// PostgreSQL jobs provider.
var ErrJobsDisabled = internalworker.ErrJobsDisabled

// FilesModule enables Aginex's official file-object HTTP API, persistence
// schema, ownership policy, and cleanup integration. Storage primitives remain
// available without enabling this module.
func FilesModule() module.Module {
	return internalapp.FilesModule()
}

// StarterExampleModule enables the repository's example products and
// product-backed dashboard. Derived applications should omit it unless they
// intentionally want the starter business model.
func StarterExampleModule() module.Module {
	return internalapp.StarterExampleModule()
}

// Definition is an immutable, reusable application composition root.
type Definition struct {
	modules       []module.Module
	fingerprint   string
	observability *observability.Recorder
}

// Define validates one complete module set and returns the composition root
// that every application process and generator must reuse.
func Define(modules ...module.Module) (Definition, error) {
	names, err := internalapp.ValidateComposition(modules...)
	if err != nil {
		return Definition{}, fmt.Errorf(
			"define application modules: %w",
			err,
		)
	}
	sum := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		return Definition{}, fmt.Errorf(
			"define application observability: %w",
			err,
		)
	}
	return Definition{
		modules:       append([]module.Module(nil), modules...),
		fingerprint:   hex.EncodeToString(sum[:]),
		observability: recorder,
	}, nil
}

// Modules returns a copy of the compiled-in application module set.
func (definition Definition) Modules() []module.Module {
	return append([]module.Module(nil), definition.modules...)
}

// Fingerprint is a stable identifier for the definition's ordered module
// names. Emit it from every process so deployment tooling can detect a
// mismatched composition root.
func (definition Definition) Fingerprint() string {
	return definition.fingerprint
}

// WithObservability returns a copy that sends provider-neutral spans and
// metrics to recorder's deployment-owned Sink. The module definition and its
// fingerprint are unchanged.
func (definition Definition) WithObservability(
	recorder *observability.Recorder,
) (Definition, error) {
	if recorder == nil {
		return Definition{}, fmt.Errorf(
			"configure application observability: recorder is required",
		)
	}
	definition.modules = append(
		[]module.Module(nil),
		definition.modules...,
	)
	definition.observability = recorder
	return definition, nil
}

// LoadConfig reads and validates the standard Aginex environment variables.
func LoadConfig() (Config, error) {
	return config.Load()
}

// WithDefaults normalizes a programmatically constructed configuration.
func WithDefaults(cfg Config) Config {
	return config.WithDefaults(cfg)
}

// OpenDatabase opens the configured portable GORM database.
func OpenDatabase(cfg DatabaseConfig) (*gorm.DB, error) {
	return database.Open(cfg)
}

// OpenDatabaseContext opens and verifies the configured database under ctx.
func OpenDatabaseContext(
	ctx context.Context,
	cfg DatabaseConfig,
) (*gorm.DB, error) {
	return database.OpenContext(ctx, cfg)
}

// NewAPI composes the HTTP runtime with this definition's exact module set.
func (definition Definition) NewAPI(
	cfg Config,
	db *gorm.DB,
) (*App, error) {
	return internalapp.NewCompositionWithModulesAndObservability(
		cfg,
		db,
		definition.observability,
		definition.Modules()...,
	)
}

// NewAPIContext composes the HTTP runtime while bounding constructor probes by
// the supplied initialization context.
func (definition Definition) NewAPIContext(
	ctx context.Context,
	cfg Config,
	db *gorm.DB,
) (*App, error) {
	return internalapp.NewCompositionWithModulesAndObservabilityContext(
		ctx,
		cfg,
		db,
		definition.observability,
		definition.Modules()...,
	)
}

// NewWorker composes the durable worker with this definition's exact module
// set. It verifies but never mutates schema state.
func (definition Definition) NewWorker(
	ctx context.Context,
	cfg Config,
	db *gorm.DB,
) (*Worker, error) {
	return internalworker.NewWithModulesAndObservability(
		ctx,
		cfg,
		db,
		definition.observability,
		definition.Modules()...,
	)
}

// MigrateUp explicitly applies core, configured opt-in, and application module
// migrations.
func (definition Definition) MigrateUp(
	ctx context.Context,
	db *sql.DB,
	cfg Config,
) error {
	return internalapp.MigrateUp(
		ctx,
		db,
		cfg,
		definition.Modules()...,
	)
}

// EnsureMigrationsCurrent checks every application-module migration bundle
// without changing schema state.
func (definition Definition) EnsureMigrationsCurrent(
	ctx context.Context,
	db *sql.DB,
	cfg Config,
) error {
	return internalapp.EnsureModulesCurrent(
		ctx,
		db,
		cfg.Database.Driver,
		definition.Modules()...,
	)
}

// Bootstrap atomically synchronizes permissions and optional administrator
// access using this definition's exact module set.
func (definition Definition) Bootstrap(
	ctx context.Context,
	db *gorm.DB,
	cfg Config,
) error {
	return internalapp.BootstrapCompositionWithModules(
		ctx,
		db,
		cfg.Bootstrap,
		definition.Modules()...,
	)
}

// BootstrapWithOptions synchronizes access for this definition and records
// the trusted execution context supplied by an embedded setup or startup
// supervisor.
func (definition Definition) BootstrapWithOptions(
	ctx context.Context,
	db *gorm.DB,
	cfg Config,
	options BootstrapOptions,
) error {
	return internalapp.BootstrapCompositionWithModulesAndOptions(
		ctx,
		db,
		cfg.Bootstrap,
		options,
		definition.Modules()...,
	)
}

// HasActiveAdministrator verifies that the configured application has a
// usable local Administrator sign-in after automatic bootstrap.
func (definition Definition) HasActiveAdministrator(
	ctx context.Context,
	db *gorm.DB,
) (bool, error) {
	return internalapp.HasActiveAdministrator(ctx, db)
}

// HasActiveAdministratorCredentials verifies the exact password identity
// submitted by the one-time HTTP Setup flow.
func (definition Definition) HasActiveAdministratorCredentials(
	ctx context.Context,
	db *gorm.DB,
	email string,
	credential string,
) (bool, error) {
	return internalapp.HasActiveAdministratorCredentials(
		ctx,
		db,
		email,
		credential,
	)
}

// BuildOpenAPI produces the contract for this definition's exact module set
// without opening runtime dependencies.
func (definition Definition) BuildOpenAPI() *huma.OpenAPI {
	document := internalapp.BuildCompositionOpenAPI(
		definition.Modules()...,
	)
	internalsetup.DocumentOpenAPI(document)
	return document
}
