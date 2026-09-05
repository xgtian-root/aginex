package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/composition"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
	"github.com/xgtian-root/aginex/backend/internal/platform/password"
	"github.com/xgtian-root/aginex/backend/internal/setup"
)

func TestSetupInitializerCreatesReadyApplicationAndRestartDoesNotReaudit(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aginex.db")
	base := config.Config{
		Environment: "test",
		HTTP: config.HTTP{
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
		WebOrigin: "http://localhost:3000",
	}
	request := setup.SetupCompleteRequest{
		Database: setup.DatabaseConfig{
			Driver: "sqlite",
			DSN:    databasePath,
		},
		Administrator: setup.AdministratorConfig{
			Email:    "admin@example.com",
			Password: "correct setup administrator password",
		},
	}
	initializer := setupApplicationInitializer{
		definition: composition.Definition(),
		base:       base,
	}
	var stages []setup.Stage
	candidate, err := initializer.InitializeApplication(
		t.Context(),
		request,
		setup.ProgressReporterFunc(func(stage setup.Stage) {
			stages = append(stages, stage)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Handler == nil || candidate.Shutdown == nil {
		t.Fatalf("setup candidate = %#v", candidate)
	}
	wantStages := []setup.Stage{
		setup.StageValidatingDatabase,
		setup.StageMigrating,
		setup.StageBootstrapping,
		setup.StageStartingApplication,
	}
	if len(stages) != len(wantStages) {
		t.Fatalf("setup stages = %v, want %v", stages, wantStages)
	}
	for index := range wantStages {
		if stages[index] != wantStages[index] {
			t.Fatalf("setup stages = %v, want %v", stages, wantStages)
		}
	}
	ready := httptest.NewRecorder()
	candidate.Handler.ServeHTTP(
		ready,
		httptest.NewRequest(http.MethodGet, "/health/ready", nil),
	)
	if ready.Code != http.StatusOK {
		t.Fatalf("candidate readiness = %d, body=%s", ready.Code, ready.Body.String())
	}
	if err := candidate.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	configured := base
	configured.Database = config.Database{
		Driver: request.Database.Driver,
		DSN:    request.Database.DSN,
	}
	restarted, err := initializeConfiguredApplication(
		t.Context(),
		composition.Definition(),
		configured,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(configured.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var audits []domain.AuditLog
	if err := db.Where("action = ?", "system:bootstrap").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("bootstrap audit count after restart = %d, want 1", len(audits))
	}
	if audits[0].Source != "http" {
		t.Fatalf("setup bootstrap source = %q, want http", audits[0].Source)
	}
}

func TestConfiguredInitializerRejectsDatabaseWithoutAdministrator(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
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
		WebOrigin: "http://localhost:3000",
	}
	if candidate, err := initializeConfiguredApplication(
		t.Context(),
		composition.Definition(),
		cfg,
	); err == nil {
		_ = candidate.Shutdown(t.Context())
		t.Fatal("configured database without an administrator became ready")
	}
}

func TestProductionSetupRejectsNonPostgreSQLBeforeOpeningDatabase(t *testing.T) {
	initializer := setupApplicationInitializer{
		base: config.Config{Environment: "production"},
	}
	for _, driver := range []string{"sqlite", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			err := initializer.TestDatabase(t.Context(), setup.DatabaseConfig{
				Driver: driver,
				DSN:    "must-not-be-opened",
			})
			if !errors.Is(err, errProductionDatabaseRequired) {
				t.Fatalf("error = %v, want production PostgreSQL gate", err)
			}
		})
	}
}

func TestSetupInitializerRetryUsesTheLatestAdministratorPassword(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aginex.db")
	base := config.WithDefaults(config.Config{
		Environment: "test",
		Session: config.Session{
			Secret: "test-session-secret-with-enough-unique-bytes",
		},
		Idempotency: config.Idempotency{Driver: "disabled"},
		Jobs:        config.Jobs{Driver: "disabled"},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	})
	initializer := setupApplicationInitializer{
		definition: composition.Definition(),
		base:       base,
	}
	request := setup.SetupCompleteRequest{
		Database: setup.DatabaseConfig{
			Driver: "sqlite",
			DSN:    databasePath,
		},
		Administrator: setup.AdministratorConfig{
			Email:    "retry-admin@example.com",
			Password: "first setup administrator password",
		},
	}
	first, err := initializer.InitializeApplication(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	request.Administrator.Password = "replacement setup administrator password"
	second, err := initializer.InitializeApplication(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(config.Database{
		Driver: request.Database.Driver,
		DSN:    request.Database.DSN,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var identity domain.UserIdentity
	if err := db.Where(
		"provider = ? AND subject = ?",
		domain.IdentityProviderPassword,
		request.Administrator.Email,
	).First(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if !password.Verify(
		identity.CredentialHash,
		request.Administrator.Password,
	) {
		t.Fatal("Setup retry did not retain the latest submitted password")
	}
	if password.Verify(
		identity.CredentialHash,
		"first setup administrator password",
	) {
		t.Fatal("Setup retry retained the password from the failed attempt")
	}
}

func TestRuntimeConfigurationDropsBootstrapCredentials(t *testing.T) {
	cfg := config.Config{
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct setup administrator password",
		},
	}
	cfg = withoutBootstrapCredentials(cfg)
	if cfg.Bootstrap.AdminEmail != "" || cfg.Bootstrap.AdminPassword != "" {
		t.Fatalf("bootstrap credentials retained in runtime config: %#v", cfg.Bootstrap)
	}
}
