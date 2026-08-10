package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/internal/composition"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/setup"
)

func TestManagedInstallationStoreMapsExistingTargetToSealedSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "aginex-config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := (managedInstallationStore{path: path}).CommitInstallation(
		t.Context(),
		setup.Installation{
			Version: setup.InstallationVersion,
			Source:  setup.InstallationSourceSetup,
			Database: setup.DatabaseConfig{
				Driver: "sqlite",
				DSN:    filepath.Join(t.TempDir(), "aginex.db"),
			},
			SessionSecret: "test-session-secret-with-enough-unique-bytes",
			InstalledAt:   time.Now().UTC(),
		},
	)
	if !errors.Is(err, setup.ErrInstallationSealed) {
		t.Fatalf("store error = %v, want sealed setup result", err)
	}
}

func TestFreshRuntimeCompletesSetupAndPermanentlySwapsRoutes(t *testing.T) {
	cfg := freshServerConfig(t)
	configFile := filepath.Join(t.TempDir(), "aginex-config.json")
	runtime, err := newManagedServerRuntime(
		t.Context(),
		config.State{
			Status:     config.StatusSetup,
			Config:     cfg,
			ConfigFile: configFile,
		},
		composition.Definition(),
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

	assertSystemMode(t, runtime.Handler(), setup.ModeSetup)
	assertHTTPStatus(t, runtime.Handler(), http.MethodGet, "/api/v1/products", nil, http.StatusNotFound)

	csrfRecorder := httptest.NewRecorder()
	csrfRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	csrfRequest.Header.Set("Origin", cfg.WebOrigin)
	runtime.Handler().ServeHTTP(csrfRecorder, csrfRequest)
	if csrfRecorder.Code != http.StatusOK {
		t.Fatalf("setup csrf status = %d, body=%s", csrfRecorder.Code, csrfRecorder.Body.String())
	}
	var csrf struct {
		Token      string `json:"token"`
		HeaderName string `json:"headerName"`
	}
	if err := json.Unmarshal(csrfRecorder.Body.Bytes(), &csrf); err != nil {
		t.Fatal(err)
	}
	if len(csrfRecorder.Result().Cookies()) != 1 {
		t.Fatalf("setup csrf cookies = %d, want 1", len(csrfRecorder.Result().Cookies()))
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
	complete.AddCookie(csrfRecorder.Result().Cookies()[0])
	completeRecorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(completeRecorder, complete)
	if completeRecorder.Code != http.StatusAccepted {
		t.Fatalf("setup completion status = %d, body=%s", completeRecorder.Code, completeRecorder.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if currentSystemMode(t, runtime.Handler()) == setup.ModeApplication {
			break
		}
		if time.Now().After(deadline) {
			status := httptest.NewRecorder()
			runtime.Handler().ServeHTTP(
				status,
				httptest.NewRequest(http.MethodGet, setup.SetupStatusPath, nil),
			)
			t.Fatalf("setup did not activate before deadline: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	installation, err := config.ReadInstallation(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Database.Source != config.DatabaseSourceManaged ||
		installation.Database.Driver != "sqlite" ||
		installation.Database.DSN != databasePath {
		t.Fatalf("persisted installation = %#v", installation.Database)
	}
	setupClosed := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(
		setupClosed,
		httptest.NewRequest(http.MethodGet, setup.SetupStatusPath, nil),
	)
	if setupClosed.Code != http.StatusNotFound ||
		setupClosed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"closed setup response = %d cache=%q body=%s",
			setupClosed.Code,
			setupClosed.Header().Get("Cache-Control"),
			setupClosed.Body.String(),
		)
	}
	assertHTTPStatus(t, runtime.Handler(), http.MethodGet, "/health/ready", nil, http.StatusOK)
}

func TestConfiguredEnvironmentRuntimeSealsMarkerWithoutDSN(t *testing.T) {
	cfg := freshServerConfig(t)
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
	runtime, err := newManagedServerRuntime(
		t.Context(),
		config.State{
			Status:                 config.StatusConfigured,
			Config:                 cfg,
			ConfigFile:             configFile,
			Installation:           &marker,
			NeedsEnvironmentMarker: true,
		},
		composition.Definition(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())
	assertSystemMode(t, runtime.Handler(), setup.ModeApplication)
	persisted, err := config.ReadInstallation(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Database.Source != config.DatabaseSourceEnvironment ||
		persisted.Database.DSN != "" {
		t.Fatalf("environment marker persisted database = %#v", persisted.Database)
	}
}

func freshServerConfig(t *testing.T) config.Config {
	t.Helper()
	return config.WithDefaults(config.Config{
		Environment: "test",
		HTTP: config.HTTP{
			Address:             ":0",
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

func assertSystemMode(t *testing.T, handler http.Handler, want setup.Mode) {
	t.Helper()
	if got := currentSystemMode(t, handler); got != want {
		t.Fatalf("system mode = %q, want %q", got, want)
	}
}

func currentSystemMode(t *testing.T, handler http.Handler) setup.Mode {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, setup.SystemModePath, nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("system mode status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response setup.SystemModeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Mode
}

func assertHTTPStatus(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	want int,
) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(method, path, bytes.NewReader(body)),
	)
	if recorder.Code != want {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, recorder.Code, want, recorder.Body.String())
	}
}
