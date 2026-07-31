package application_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/framework/application"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/framework/observability"
	"github.com/xgtian-root/aginex/framework/services"
	"gorm.io/gorm"
)

type externalResponse struct {
	Message string `json:"message"`
}

type externalModule struct {
	lifecycleRuntime *bool
}

type namedEmptyModule string

func (item namedEmptyModule) Name() string {
	return string(item)
}

func (namedEmptyModule) Register(*module.Registry) error {
	return nil
}

func (externalModule) Name() string {
	return "external"
}

func (item externalModule) Register(registry *module.Registry) error {
	if err := registry.RegisterOperation(module.OperationDefinition{
		ID:     "readExternal",
		Method: module.MethodGet,
		Path:   "/api/v1/external",
		Public: true,
	}); err != nil {
		return err
	}
	if err := registry.RegisterAPIContract(module.APIContract{
		OperationID:   "readExternal",
		Summary:       "Read an external module",
		Tag:           "External",
		SuccessStatus: http.StatusOK,
		ResponseDTO:   reflect.TypeFor[externalResponse](),
		ErrorStatuses: []int{http.StatusInternalServerError},
	}); err != nil {
		return err
	}
	if err := registry.RegisterHTTPRoute(module.HTTPRoute{
		OperationID: "readExternal",
		Handler: func(c *gin.Context) {
			runtime, ok := services.RuntimeFromContext(c.Request.Context())
			if !ok ||
				runtime.Database == nil ||
				runtime.Writes == nil ||
				runtime.Storage == nil ||
				runtime.Observability == nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.JSON(http.StatusOK, externalResponse{Message: "external"})
		},
	}); err != nil {
		return err
	}
	if item.lifecycleRuntime == nil {
		return nil
	}
	return registry.RegisterLifecycleHook(module.LifecycleHook{
		Name: "external-runtime",
		Start: func(ctx context.Context) error {
			runtime, ok := services.RuntimeFromContext(ctx)
			if !ok ||
				runtime.Database == nil ||
				runtime.Writes == nil ||
				runtime.Storage == nil ||
				runtime.Observability == nil {
				return errors.New("runtime services are unavailable")
			}
			*item.lifecycleRuntime = true
			return nil
		},
	})
}

func TestDefinitionIsAReusableExternalCompositionRoot(t *testing.T) {
	lifecycleRuntime := false
	definition, err := application.Define(externalModule{
		lifecycleRuntime: &lifecycleRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Fingerprint() == "" {
		t.Fatal("definition fingerprint is empty")
	}
	snapshot := definition.Modules()
	snapshot[0] = nil
	if definition.Modules()[0] == nil {
		t.Fatal("definition module snapshot is mutable")
	}
	sink := &capturingSink{}
	telemetry, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = definition.WithObservability(telemetry)
	if err != nil {
		t.Fatal(err)
	}

	cfg := application.Config{
		Environment: "test",
		HTTP: application.HTTPConfig{
			Address: ":0",
		},
		Database: application.DatabaseConfig{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "external.db"),
		},
		Session: application.SessionConfig{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Storage: application.StorageConfig{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	cfg = application.WithDefaults(cfg)
	db, err := application.OpenDatabase(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := definition.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	if err := definition.Bootstrap(t.Context(), db, cfg); err != nil {
		t.Fatal(err)
	}
	api, err := definition.NewAPI(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := api.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown API: %v", err)
		}
	})
	if !lifecycleRuntime {
		t.Fatal("module lifecycle hook did not receive runtime services")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/external", nil)
	recorder := httptest.NewRecorder()
	api.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"external status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if !sink.hasSpan("GET /api/v1/external") {
		t.Fatal("external request did not produce a route-level server span")
	}
	if !sink.hasMetric("http.server.requests") {
		t.Fatal("external request did not produce HTTP request metrics")
	}
	secretPath := "/unmatched/TOP-SECRET-user-path"
	unmatchedRequest := httptest.NewRequest(
		http.MethodGet,
		secretPath,
		nil,
	)
	unmatchedResponse := httptest.NewRecorder()
	api.Handler().ServeHTTP(unmatchedResponse, unmatchedRequest)
	if sink.contains("TOP-SECRET-user-path") {
		t.Fatal("raw request path escaped into telemetry")
	}
	if definition.BuildOpenAPI().Paths["/api/v1/external"].Get == nil {
		t.Fatal("external operation is missing from OpenAPI")
	}
	for _, businessPath := range []string{
		"/api/v1/products",
		"/api/v1/dashboard/summary",
		"/api/v1/files",
	} {
		if definition.BuildOpenAPI().Paths[businessPath] != nil {
			t.Fatalf(
				"external-only definition inherited %q",
				businessPath,
			)
		}
	}
	if _, err := definition.NewWorker(
		context.Background(),
		cfg,
		db,
	); err == nil {
		t.Fatal("worker unexpectedly started with jobs disabled")
	}
}

func TestEmptyDefinitionHasNoBusinessSchemaOrEndpoints(t *testing.T) {
	definition, err := application.Define()
	if err != nil {
		t.Fatal(err)
	}
	cfg := definitionTestConfig(t, "zero-business.db")
	db, sqlDB := openDefinitionDatabase(t, cfg)
	if err := definition.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"products", "file_objects"} {
		if sqliteTableExists(t, sqlDB, table) {
			t.Fatalf("empty definition created business table %q", table)
		}
	}

	document := definition.BuildOpenAPI()
	for _, schema := range []string{
		"ProductRequest",
		"ProductResponse",
		"FileResponse",
		"UploadIntentRequest",
		"UploadIntentResponse",
	} {
		if document.Components.Schemas.Map()[schema] != nil {
			t.Fatalf(
				"empty definition exposes business schema %q",
				schema,
			)
		}
	}
	api, err := definition.NewAPI(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/products",
		"/api/v1/dashboard/summary",
		"/api/v1/files",
	} {
		if document.Paths[path] != nil {
			t.Fatalf("empty definition documents business path %q", path)
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf(
				"empty definition %s status = %d, want 404",
				path,
				response.Code,
			)
		}
	}
}

func TestDefineValidatesAndFingerprintsTheCompleteCoreComposition(
	t *testing.T,
) {
	zero, err := application.Define()
	if err != nil {
		t.Fatal(err)
	}
	starter, err := application.Define(
		application.FilesModule(),
		application.StarterExampleModule(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if zero.Fingerprint() == "" {
		t.Fatal("zero-business core fingerprint is empty")
	}
	if zero.Fingerprint() == starter.Fingerprint() {
		t.Fatal("explicit bundled modules did not change the fingerprint")
	}
	if _, err := application.Define(namedEmptyModule("access")); err == nil ||
		!strings.Contains(err.Error(), `module "access"`) {
		t.Fatalf(
			"reserved core module conflict error = %v",
			err,
		)
	}
}

func TestStarterDefinitionExplicitlyAddsBusinessSchemaAndEndpoints(
	t *testing.T,
) {
	definition, err := application.Define(
		application.FilesModule(),
		application.StarterExampleModule(),
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg := definitionTestConfig(t, "starter.db")
	_, sqlDB := openDefinitionDatabase(t, cfg)
	if err := definition.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	if err := definition.EnsureMigrationsCurrent(
		t.Context(),
		sqlDB,
		cfg,
	); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"products", "file_objects"} {
		if !sqliteTableExists(t, sqlDB, table) {
			t.Fatalf("starter definition did not create %q", table)
		}
	}
	document := definition.BuildOpenAPI()
	for _, path := range []string{
		"/api/v1/products",
		"/api/v1/dashboard/summary",
		"/api/v1/files",
	} {
		if document.Paths[path] == nil {
			t.Fatalf("starter definition is missing %q", path)
		}
	}
}

func TestFilesModuleAloneOwnsProductionDurableJobsGate(t *testing.T) {
	cfg := definitionTestConfig(t, "production.db")
	cfg.Environment = "production"
	cfg.HTTP.PublicURL = "https://api.example.com"
	cfg.Session.Secret = "9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!"
	cfg.Session.Secure = true
	cfg.WebOrigin = "https://admin.example.com"
	cfg.WebOrigins = []string{"https://admin.example.com"}
	cfg.Jobs.Driver = "disabled"

	zero, err := application.Define()
	if err != nil {
		t.Fatal(err)
	}
	db, sqlDB := openDefinitionDatabase(t, cfg)
	if err := zero.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := zero.NewAPI(cfg, db); err != nil {
		t.Fatalf(
			"zero-business production API required file jobs: %v",
			err,
		)
	}

	files, err := application.Define(application.FilesModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := files.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := files.NewAPI(cfg, db); err == nil ||
		!strings.Contains(err.Error(), "AGINEX_JOBS_DRIVER=postgres") {
		t.Fatalf(
			"files production API error = %v, want durable jobs gate",
			err,
		)
	}
}

func definitionTestConfig(t *testing.T, databaseName string) application.Config {
	t.Helper()
	return application.WithDefaults(application.Config{
		Environment: "test",
		HTTP: application.HTTPConfig{
			Address: ":0",
		},
		Database: application.DatabaseConfig{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), databaseName),
		},
		Session: application.SessionConfig{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Storage: application.StorageConfig{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	})
}

func openDefinitionDatabase(
	t *testing.T,
	cfg application.Config,
) (*gorm.DB, *sql.DB) {
	t.Helper()
	db, err := application.OpenDatabase(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close definition database: %v", err)
		}
	})
	return db, sqlDB
}

func sqliteTableExists(
	t *testing.T,
	db *sql.DB,
	table string,
) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

type capturingSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (sink *capturingSink) RecordSpan(record observability.SpanRecord) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, record)
}

func (sink *capturingSink) RecordMetric(metric observability.Metric) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.metrics = append(sink.metrics, metric)
}

func (sink *capturingSink) hasSpan(name string) bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, span := range sink.spans {
		if span.Name == name {
			return true
		}
	}
	return false
}

func (sink *capturingSink) hasMetric(name string) bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, metric := range sink.metrics {
		if metric.Name == name {
			return true
		}
	}
	return false
}

func (sink *capturingSink) contains(candidate string) bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, span := range sink.spans {
		if strings.Contains(span.Name, candidate) ||
			strings.Contains(span.Error, candidate) {
			return true
		}
		for key, value := range span.Attributes.Values() {
			if strings.Contains(key, candidate) ||
				strings.Contains(value, candidate) {
				return true
			}
		}
	}
	for _, metric := range sink.metrics {
		if strings.Contains(metric.Name, candidate) {
			return true
		}
		for key, value := range metric.Attributes.Values() {
			if strings.Contains(key, candidate) ||
				strings.Contains(value, candidate) {
				return true
			}
		}
	}
	return false
}
