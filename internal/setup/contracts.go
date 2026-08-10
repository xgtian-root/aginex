package setup

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	GetSystemModeOperationID     = "getSystemMode"
	GetSetupStatusOperationID    = "getSetupStatus"
	TestSetupDatabaseOperationID = "testSetupDatabase"
	CompleteSetupOperationID     = "completeSetup"
)

const (
	SystemModePath        = "/api/v1/system/mode"
	SetupStatusPath       = "/api/v1/setup/status"
	SetupDatabaseTestPath = "/api/v1/setup/database/test"
	SetupCompletePath     = "/api/v1/setup/complete"
)

// Mode is the mutually exclusive HTTP surface exposed by a Supervisor.
type Mode string

const (
	ModeSetup       Mode = "setup"
	ModeApplication Mode = "application"
)

// Status describes the state of the one-time setup attempt. A successfully
// activated application no longer exposes the setup status endpoint.
type Status string

const (
	StatusRequired     Status = "required"
	StatusInitializing Status = "initializing"
	StatusFailed       Status = "failed"
)

// Stage is intentionally a safe, finite progress vocabulary. It must never be
// populated from a database driver error, DSN, SQL statement, or user input.
type Stage string

const (
	StageWaiting                 Stage = "waiting"
	StageValidatingDatabase      Stage = "validating_database"
	StageMigrating               Stage = "migrating"
	StageBootstrapping           Stage = "bootstrapping"
	StageStartingApplication     Stage = "starting_application"
	StagePersistingConfiguration Stage = "persisting_configuration"
	StageActivatingApplication   Stage = "activating_application"
)

type SystemModeResponse struct {
	Mode Mode `json:"mode" enum:"setup,application"`
}

type SetupStatusResponse struct {
	Status Status `json:"status" enum:"required,initializing,failed"`
	Stage  Stage  `json:"stage" enum:"waiting,validating_database,migrating,bootstrapping,starting_application,persisting_configuration,activating_application"`
	Code   string `json:"code,omitempty"`
}

// DatabaseInput is the browser-facing Setup database configuration. Exactly
// one driver-specific object must be present and it must match Driver. The
// backend resolves this untrusted value into an opaque DatabaseConfig before
// database verification, initialization, or persistence.
type DatabaseInput struct {
	Driver   string                 `json:"driver" enum:"sqlite,postgres,mysql"`
	SQLite   *SQLiteDatabaseInput   `json:"sqlite,omitempty"`
	Postgres *PostgresDatabaseInput `json:"postgres,omitempty"`
	MySQL    *MySQLDatabaseInput    `json:"mysql,omitempty"`
}

type SQLiteDatabaseInput struct {
	Directory string `json:"directory" minLength:"1" maxLength:"4096"`
	Filename  string `json:"filename" minLength:"1" maxLength:"255"`
}

type PostgresDatabaseInput struct {
	Host     string `json:"host" minLength:"1" maxLength:"255"`
	Port     int    `json:"port" minimum:"1" maximum:"65535"`
	Database string `json:"database" minLength:"1" maxLength:"128"`
	Username string `json:"username" minLength:"1" maxLength:"128"`
	Password string `json:"password" minLength:"1" maxLength:"1024" writeOnly:"true"`
	SSLMode  string `json:"sslMode" enum:"disable,require,verify-ca,verify-full"`
}

type MySQLDatabaseInput struct {
	Host     string `json:"host" minLength:"1" maxLength:"255"`
	Port     int    `json:"port" minimum:"1" maximum:"65535"`
	Database string `json:"database" minLength:"1" maxLength:"128"`
	Username string `json:"username" minLength:"1" maxLength:"128"`
	Password string `json:"password" minLength:"1" maxLength:"1024" writeOnly:"true"`
	TLSMode  string `json:"tlsMode" enum:"disabled,required,skip-verify"`
}

// DatabaseConfig is the trusted internal representation passed beyond the
// HTTP boundary. Its DSN is assembled by Setup and remains the persisted
// installation/runtime format for backward compatibility.
type DatabaseConfig struct {
	Driver string `json:"driver" enum:"sqlite,postgres,mysql"`
	DSN    string `json:"dsn" minLength:"1" maxLength:"8192" writeOnly:"true"`
}

type AdministratorConfig struct {
	Email    string `json:"email" format:"email" maxLength:"320"`
	Password string `json:"password" writeOnly:"true"`
}

type SetupDatabaseTestRequest struct {
	Database DatabaseInput `json:"database"`
}

type SetupDatabaseTestResponse struct {
	Status string `json:"status" enum:"ok"`
}

// SetupCompleteInput is the public request body accepted by the Setup HTTP
// endpoint. SetupCompleteRequest below is its resolved internal counterpart.
type SetupCompleteInput struct {
	Database      DatabaseInput       `json:"database"`
	Administrator AdministratorConfig `json:"administrator"`
}

// SetupCompleteRequest is a trusted request containing a backend-assembled
// database DSN. It must never be decoded directly from an HTTP request.
type SetupCompleteRequest struct {
	Database      DatabaseConfig
	Administrator AdministratorConfig
}

type SetupAcceptedResponse struct {
	Status Status `json:"status" enum:"initializing"`
}

// RequestMetadata contains only server-validated attribution which is safe to
// carry into the detached initialization context for bootstrap auditing.
type RequestMetadata struct {
	RequestID string
	IPAddress string
}

type requestMetadataContextKey struct{}

func contextWithRequestMetadata(
	ctx context.Context,
	metadata RequestMetadata,
) context.Context {
	return context.WithValue(ctx, requestMetadataContextKey{}, metadata)
}

func RequestMetadataFromContext(ctx context.Context) (RequestMetadata, bool) {
	metadata, ok := ctx.Value(requestMetadataContextKey{}).(RequestMetadata)
	return metadata, ok
}

// Installation is the persistence boundary between the setup supervisor and
// the deployment-owned configuration store. The store is responsible for
// translating this value into its versioned on-disk representation.
type Installation struct {
	Version       int
	Source        string
	Database      DatabaseConfig
	SessionSecret string
	InstalledAt   time.Time
}

const InstallationVersion = 1

const InstallationSourceSetup = "setup"

// ErrInstallationSealed marks a persistence result at or beyond the exclusive
// publication boundary. The current process must close Setup permanently even
// though it cannot safely activate the submitted candidate.
var ErrInstallationSealed = errors.New("installation configuration is sealed")

const (
	FailureDatabaseUnavailable       = "SETUP_DATABASE_UNAVAILABLE"
	FailureApplicationInitialization = "SETUP_APPLICATION_INITIALIZATION_FAILED"
	FailureConfigurationCommit       = "SETUP_CONFIGURATION_COMMIT_FAILED"
	FailureInitialization            = "SETUP_INITIALIZATION_FAILED"
)

// Candidate is a fully initialized, started application which is safe to
// activate after its Installation has been committed. Shutdown is called when
// activation cannot complete or when the owning Supervisor shuts down.
type Candidate struct {
	Handler       http.Handler
	SessionSecret string
	Shutdown      func(context.Context) error
}

type DatabaseTester interface {
	TestDatabase(context.Context, DatabaseConfig) error
}

type DatabaseTesterFunc func(context.Context, DatabaseConfig) error

func (f DatabaseTesterFunc) TestDatabase(
	ctx context.Context,
	database DatabaseConfig,
) error {
	return f(ctx, database)
}

// ProgressReporter accepts only predefined stages. Invalid or stale reports
// are ignored by the Supervisor.
type ProgressReporter interface {
	Report(Stage)
}

type ProgressReporterFunc func(Stage)

func (f ProgressReporterFunc) Report(stage Stage) {
	f(stage)
}

type ApplicationInitializer interface {
	InitializeApplication(
		context.Context,
		SetupCompleteRequest,
		ProgressReporter,
	) (Candidate, error)
}

type ApplicationInitializerFunc func(
	context.Context,
	SetupCompleteRequest,
	ProgressReporter,
) (Candidate, error)

func (f ApplicationInitializerFunc) InitializeApplication(
	ctx context.Context,
	request SetupCompleteRequest,
	reporter ProgressReporter,
) (Candidate, error) {
	return f(ctx, request, reporter)
}

type InstallationStore interface {
	CommitInstallation(context.Context, Installation) error
}

type InstallationStoreFunc func(context.Context, Installation) error

func (f InstallationStoreFunc) CommitInstallation(
	ctx context.Context,
	installation Installation,
) error {
	return f(ctx, installation)
}
