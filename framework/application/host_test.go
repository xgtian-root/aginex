package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/setup"
)

var _ func(Definition, context.Context) error = Definition.RunAPI

func TestRunAPIRejectsInvalidContextBeforeLoadingConfiguration(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.RunAPI(nil); err == nil ||
		!strings.Contains(err.Error(), "context is required") {
		t.Fatalf("nil context error = %v", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := definition.RunAPI(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context cancellation", err)
	}
}

func TestAPIHostRuntimeCompletesBrowserSetup(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	cfg := freshAPIHostConfig(t)
	configFile := filepath.Join(t.TempDir(), "aginex-config.json")
	runtime, err := newAPIHostRuntime(
		t.Context(),
		config.State{
			Status:     config.StatusSetup,
			Config:     cfg,
			ConfigFile: configFile,
		},
		definition,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()
		if err := runtime.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown setup runtime: %v", err)
		}
	})

	assertAPIHostMode(t, runtime.Handler(), setup.ModeSetup)
	csrfRecorder := httptest.NewRecorder()
	csrfRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	csrfRequest.Header.Set("Origin", cfg.WebOrigin)
	runtime.Handler().ServeHTTP(csrfRecorder, csrfRequest)
	if csrfRecorder.Code != http.StatusOK {
		t.Fatalf(
			"setup csrf status = %d, body=%s",
			csrfRecorder.Code,
			csrfRecorder.Body.String(),
		)
	}
	var csrf struct {
		Token      string `json:"token"`
		HeaderName string `json:"headerName"`
	}
	if err := json.Unmarshal(csrfRecorder.Body.Bytes(), &csrf); err != nil {
		t.Fatal(err)
	}
	cookies := csrfRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("setup csrf cookies = %d, want 1", len(cookies))
	}

	databasePath := filepath.Join(t.TempDir(), "configured.db")
	body, err := json.Marshal(setup.SetupCompleteInput{
		Database: setup.DatabaseInput{
			Driver: "sqlite",
			SQLite: &setup.SQLiteDatabaseInput{
				Directory: filepath.Dir(databasePath),
				Filename:  filepath.Base(databasePath),
			},
		},
		Administrator: setup.AdministratorConfig{
			Email:    "admin@example.com",
			Password: "correct setup administrator password",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := httptest.NewRequest(
		http.MethodPost,
		setup.SetupCompletePath,
		bytes.NewReader(body),
	)
	complete.Header.Set("Content-Type", "application/json")
	complete.Header.Set("Origin", cfg.WebOrigin)
	complete.Header.Set(csrf.HeaderName, csrf.Token)
	complete.AddCookie(cookies[0])
	completeRecorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(completeRecorder, complete)
	if completeRecorder.Code != http.StatusAccepted {
		t.Fatalf(
			"setup completion status = %d, body=%s",
			completeRecorder.Code,
			completeRecorder.Body.String(),
		)
	}

	deadline := time.Now().Add(10 * time.Second)
	for currentAPIHostMode(t, runtime.Handler()) != setup.ModeApplication {
		if time.Now().After(deadline) {
			t.Fatal("setup did not activate before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertAPIHostStatus(
		t,
		runtime.Handler(),
		http.MethodGet,
		"/health/ready",
		http.StatusOK,
	)

	installation, err := config.ReadInstallation(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Database.Source != config.DatabaseSourceManaged ||
		installation.Database.Driver != "sqlite" ||
		installation.Database.DSN != databasePath {
		t.Fatalf("persisted installation = %#v", installation.Database)
	}
}

func TestConfiguredAPIHostSealsEnvironmentMarkerWithoutDSN(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	cfg := freshAPIHostConfig(t)
	cfg.Database = config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "configured.db"),
	}
	cfg.Bootstrap = config.Bootstrap{
		AdminEmail:    "admin@example.com",
		AdminPassword: "correct setup administrator password",
	}
	marker, err := config.NewEnvironmentInstallation(
		cfg.Database.Driver,
		cfg.Session.Secret,
	)
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "aginex-config.json")
	runtime, err := newAPIHostRuntime(
		t.Context(),
		config.State{
			Status:                 config.StatusConfigured,
			Config:                 cfg,
			ConfigFile:             configFile,
			Installation:           &marker,
			NeedsEnvironmentMarker: true,
		},
		definition,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtime.Shutdown(t.Context()); err != nil {
			t.Errorf("shutdown configured runtime: %v", err)
		}
	}()

	assertAPIHostMode(t, runtime.Handler(), setup.ModeApplication)
	persisted, err := config.ReadInstallation(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Database.Source != config.DatabaseSourceEnvironment ||
		persisted.Database.DSN != "" {
		t.Fatalf(
			"environment marker persisted database = %#v",
			persisted.Database,
		)
	}
}

func TestAPIHostProductionSetupRejectsNonPostgreSQLBeforeOpen(
	t *testing.T,
) {
	initializer := apiHostSetupInitializer{
		base: config.Config{Environment: "production"},
	}
	for _, driver := range []string{"sqlite", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			err := initializer.TestDatabase(
				t.Context(),
				setup.DatabaseConfig{
					Driver: driver,
					DSN:    "must-not-be-opened",
				},
			)
			if !errors.Is(err, errAPIHostProductionDatabaseRequired) {
				t.Fatalf(
					"error = %v, want production PostgreSQL gate",
					err,
				)
			}
		})
	}
}

type testAPIHostHTTPRuntime struct {
	shutdown func(context.Context) error
	close    func() error
}

func (runtime testAPIHostHTTPRuntime) Shutdown(ctx context.Context) error {
	return runtime.shutdown(ctx)
}

func (runtime testAPIHostHTTPRuntime) Close() error {
	return runtime.close()
}

type testAPIHostLifecycle struct {
	shutdown func(context.Context) error
}

func (runtime testAPIHostLifecycle) Shutdown(ctx context.Context) error {
	return runtime.shutdown(ctx)
}

func TestShutdownAPIHostForceClosesBeforeFreshLifecycleBudget(t *testing.T) {
	var forceClosed atomic.Bool
	var lifecycleCalls atomic.Int32
	err := shutdownAPIHost(
		testAPIHostHTTPRuntime{
			shutdown: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
			close: func() error {
				forceClosed.Store(true)
				return nil
			},
		},
		testAPIHostLifecycle{
			shutdown: func(ctx context.Context) error {
				lifecycleCalls.Add(1)
				if !forceClosed.Load() {
					t.Fatal("lifecycle stopped before HTTP force close")
				}
				if err := ctx.Err(); err != nil {
					t.Fatalf(
						"lifecycle received expired HTTP context: %v",
						err,
					)
				}
				return nil
			},
		},
		5*time.Millisecond,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if got := lifecycleCalls.Load(); got != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", got)
	}
}

func TestAPIHostLoggerRedactsOpaqueErrorsAndSecrets(t *testing.T) {
	var output bytes.Buffer
	logger := newAPIHostLogger(&output)
	logger.Error(
		"host failed",
		"error", errors.New("postgres://user:secret@example.invalid/database"),
		"session_secret", "do-not-log-this-secret",
	)
	logged := output.String()
	if strings.Contains(logged, "postgres://") ||
		strings.Contains(logged, "do-not-log-this-secret") {
		t.Fatalf("sensitive value escaped into host log: %s", logged)
	}
	if count := strings.Count(logged, "[REDACTED]"); count != 2 {
		t.Fatalf("redacted values = %d, want 2; log=%s", count, logged)
	}
}

func freshAPIHostConfig(t *testing.T) config.Config {
	t.Helper()
	return config.WithDefaults(config.Config{
		Environment: "test",
		HTTP: config.HTTP{
			Address:             "127.0.0.1:0",
			PublicURL:           "http://localhost:8080",
			ShutdownGracePeriod: time.Second,
		},
		Session: config.Session{
			Secret: "test-session-secret-with-enough-unique-bytes",
		},
		Idempotency: config.Idempotency{Driver: "disabled"},
		Jobs:        config.Jobs{Driver: "disabled"},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin:  "http://localhost:3000",
		WebOrigins: []string{"http://localhost:3000"},
	})
}

func assertAPIHostMode(
	t *testing.T,
	handler http.Handler,
	want setup.Mode,
) {
	t.Helper()
	if got := currentAPIHostMode(t, handler); got != want {
		t.Fatalf("system mode = %q, want %q", got, want)
	}
}

func currentAPIHostMode(t *testing.T, handler http.Handler) setup.Mode {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, setup.SystemModePath, nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"system mode status = %d, body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var response setup.SystemModeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Mode
}

func assertAPIHostStatus(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	want int,
) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(method, path, nil),
	)
	if recorder.Code != want {
		t.Fatalf(
			"%s %s status = %d, want %d; body=%s",
			method,
			path,
			recorder.Code,
			want,
			recorder.Body.String(),
		)
	}
}
