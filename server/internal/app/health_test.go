package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/internal/config"
)

func TestReadinessFailsWhenSchemaFallsBehindAfterStartup(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Ready(t.Context()); err != nil {
		t.Fatalf("initial callable readiness: %v", err)
	}

	ready := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	readyRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(readyRecorder, ready)
	if readyRecorder.Code != http.StatusOK {
		t.Fatalf("initial readiness status = %d, body = %s", readyRecorder.Code, readyRecorder.Body.String())
	}

	if err := db.Exec(
		"UPDATE goose_db_version SET is_applied = false WHERE version_id = ?",
		6,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := server.Ready(t.Context()); err == nil {
		t.Fatal("callable readiness accepted a behind-schema database")
	}

	notReady := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	notReadyRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(notReadyRecorder, notReady)
	if notReadyRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"behind-schema readiness status = %d, want %d; body = %s",
			notReadyRecorder.Code,
			http.StatusServiceUnavailable,
			notReadyRecorder.Body.String(),
		)
	}

	live := httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	liveRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(liveRecorder, live)
	if liveRecorder.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, body = %s", liveRecorder.Code, liveRecorder.Body.String())
	}

	operationalLive := httptest.NewRequest(
		http.MethodGet,
		"/health/live",
		nil,
	)
	operationalLiveRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		operationalLiveRecorder,
		operationalLive,
	)
	if operationalLiveRecorder.Code != http.StatusOK {
		t.Fatalf(
			"operational liveness status = %d, body = %s",
			operationalLiveRecorder.Code,
			operationalLiveRecorder.Body.String(),
		)
	}
}

func TestModuleReadinessChecksAreBoundedAndFailClosed(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	dependencyError := errors.New("dependency unavailable")
	server, err := NewWithModules(
		cfg,
		db,
		testModule{
			name: "required-dependency",
			register: func(registry *module.Registry) error {
				return registry.RegisterReadinessCheck(
					module.ReadinessCheck{
						Name:        "posta.required",
						Requirement: module.ReadinessRequired,
						Timeout:     20 * time.Millisecond,
						Check: func(context.Context) error {
							return dependencyError
						},
					},
				)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Ready(t.Context()); !errors.Is(err, dependencyError) {
		t.Fatalf("callable required readiness error = %v", err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/health/ready",
		nil,
	)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"required dependency status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), dependencyError.Error()) {
		t.Fatalf(
			"readiness response exposed internal error: %s",
			recorder.Body.String(),
		)
	}
}

func TestOptionalModuleReadinessDoesNotRemoveService(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	server, err := NewWithModules(
		cfg,
		db,
		testModule{
			name: "optional-dependency",
			register: func(registry *module.Registry) error {
				return registry.RegisterReadinessCheck(
					module.ReadinessCheck{
						Name:        "posta.optional",
						Requirement: module.ReadinessOptional,
						Timeout:     20 * time.Millisecond,
						Check: func(context.Context) error {
							return errors.New("optional dependency unavailable")
						},
					},
				)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Ready(t.Context()); err != nil {
		t.Fatalf("optional callable readiness: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/health/ready",
		nil,
	)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"optional dependency status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}
