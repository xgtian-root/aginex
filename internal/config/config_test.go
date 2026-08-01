package config

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/framework/httpx"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AGINEX_ENV", "development")
	t.Setenv("AGINEX_DATABASE_DRIVER", "sqlite")
	t.Setenv("AGINEX_DATABASE_DSN", "test.db")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Fatalf("driver = %q", cfg.Database.Driver)
	}
	if cfg.HTTP.Address != ":8080" {
		t.Fatalf("address = %q", cfg.HTTP.Address)
	}
	if cfg.Session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("same-site = %d", cfg.Session.SameSite)
	}
	if cfg.Session.CookieName != httpx.SessionCookieName ||
		cfg.Session.CSRFCookie != httpx.CSRFCookieName ||
		cfg.Session.CSRFHeader != httpx.CSRFHeaderName {
		t.Fatalf("csrf config = cookie %q header %q", cfg.Session.CSRFCookie, cfg.Session.CSRFHeader)
	}
	if cfg.HTTP.MaxBodyBytes != 12<<20 || cfg.HTTP.MaxHeaderBytes != 1<<20 {
		t.Fatalf("HTTP limits = %#v", cfg.HTTP)
	}
	if cfg.RateLimit.LoginLimit != 10 || cfg.RateLimit.LoginWindow != 5*time.Minute {
		t.Fatalf("login rate limit = %#v", cfg.RateLimit)
	}
	if cfg.Jobs.Driver != "disabled" || cfg.Jobs.WorkerID != "aginex-worker" {
		t.Fatalf("jobs = %#v", cfg.Jobs)
	}
	if cfg.Idempotency.Driver != "database" ||
		cfg.Idempotency.LeaseDuration != 30*time.Second ||
		cfg.Idempotency.TTL != 24*time.Hour {
		t.Fatalf("idempotency = %#v", cfg.Idempotency)
	}
}

func TestProductionRequiresSessionSecret(t *testing.T) {
	t.Setenv("AGINEX_ENV", "production")
	t.Setenv("AGINEX_DATABASE_DRIVER", "postgres")
	t.Setenv("AGINEX_JOBS_DRIVER", "postgres")
	t.Setenv("AGINEX_SESSION_SECRET", "short")
	t.Setenv("AGINEX_SESSION_SECURE", "true")
	t.Setenv("AGINEX_API_PUBLIC_URL", "https://api.example.com")
	t.Setenv("AGINEX_WEB_ORIGINS", "https://admin.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected production secret validation to fail")
	}
}

func TestProductionRejectsInsecureCookiesAndOrigins(t *testing.T) {
	t.Setenv("AGINEX_ENV", "production")
	t.Setenv("AGINEX_DATABASE_DRIVER", "postgres")
	t.Setenv("AGINEX_JOBS_DRIVER", "postgres")
	t.Setenv(
		"AGINEX_SESSION_SECRET",
		"9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!",
	)
	t.Setenv("AGINEX_SESSION_SECURE", "false")
	t.Setenv("AGINEX_API_PUBLIC_URL", "http://api.example.com")
	t.Setenv("AGINEX_WEB_ORIGINS", "http://admin.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected insecure production configuration to fail")
	}
}

func TestProductionCoreConfigurationDoesNotRequireDurableJobs(t *testing.T) {
	t.Setenv("AGINEX_ENV", "production")
	t.Setenv("AGINEX_DATABASE_DRIVER", "postgres")
	t.Setenv("AGINEX_DATABASE_DSN", "postgres://aginex@example.com/aginex")
	t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
	t.Setenv(
		"AGINEX_SESSION_SECRET",
		"9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!",
	)
	t.Setenv("AGINEX_SESSION_SECURE", "true")
	t.Setenv("AGINEX_API_PUBLIC_URL", "https://api.example.com")
	t.Setenv("AGINEX_WEB_ORIGINS", "https://admin.example.com")

	if _, err := Load(); err != nil {
		t.Fatalf("Load zero-business production configuration: %v", err)
	}
}

func TestLoadExplicitOriginAllowlistAndTrustedProxies(t *testing.T) {
	t.Setenv("AGINEX_ENV", "development")
	t.Setenv("AGINEX_WEB_ORIGINS", "https://admin.example.com, https://ops.example.com")
	t.Setenv("AGINEX_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedWebOrigins()) != 2 || cfg.WebOrigin != "https://admin.example.com" {
		t.Fatalf("origins = %#v", cfg.AllowedWebOrigins())
	}
	if len(cfg.HTTP.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %#v", cfg.HTTP.TrustedProxies)
	}
}

func TestLoadRejectsUnsafeTrustedProxyRanges(t *testing.T) {
	for _, proxies := range []string{
		"0.0.0.0/0",
		"::/0",
		"127.0.0.1,not-an-address",
	} {
		t.Run(proxies, func(t *testing.T) {
			t.Setenv("AGINEX_TRUSTED_PROXIES", proxies)
			if _, err := Load(); err == nil ||
				!strings.Contains(err.Error(), "AGINEX_TRUSTED_PROXIES") {
				t.Fatalf("unsafe trusted proxies error = %v", err)
			}
		})
	}
}

func TestLoadRejectsConfigurableBrowserSecurityProtocol(t *testing.T) {
	for key, value := range map[string]string{
		"AGINEX_SESSION_COOKIE": "custom_session",
		"AGINEX_CSRF_COOKIE":    "custom_csrf",
		"AGINEX_CSRF_HEADER":    "X-Custom-CSRF",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := Load(); err == nil ||
				!strings.Contains(err.Error(), "published API contract") {
				t.Fatalf("custom protocol value error = %v", err)
			}
		})
	}
}

func TestProductionRejectsExampleCredentials(t *testing.T) {
	base := func(t *testing.T) {
		t.Helper()
		t.Setenv("AGINEX_ENV", "production")
		t.Setenv("AGINEX_DATABASE_DRIVER", "postgres")
		t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
		t.Setenv("AGINEX_SESSION_SECURE", "true")
		t.Setenv(
			"AGINEX_API_PUBLIC_URL",
			"https://api.example.com",
		)
		t.Setenv(
			"AGINEX_WEB_ORIGINS",
			"https://admin.example.com",
		)
	}
	t.Run("session placeholder", func(t *testing.T) {
		base(t)
		t.Setenv(
			"AGINEX_SESSION_SECRET",
			"replace-with-at-least-32-random-bytes",
		)
		if _, err := Load(); err == nil {
			t.Fatal("production accepted the example session secret")
		}
	})
	t.Run("bootstrap placeholder", func(t *testing.T) {
		base(t)
		t.Setenv(
			"AGINEX_SESSION_SECRET",
			"9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!",
		)
		t.Setenv(
			"AGINEX_BOOTSTRAP_ADMIN_EMAIL",
			"admin@example.com",
		)
		t.Setenv(
			"AGINEX_BOOTSTRAP_ADMIN_PASSWORD",
			"change-me-before-production",
		)
		if _, err := Load(); err == nil ||
			!strings.Contains(
				err.Error(),
				"AGINEX_BOOTSTRAP_ADMIN_PASSWORD",
			) {
			t.Fatalf("production bootstrap placeholder error = %v", err)
		}
	})
}

func TestLoadRejectsInvalidSameSiteAndLimits(t *testing.T) {
	t.Setenv("AGINEX_SESSION_SAME_SITE", "automatic")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid SameSite to fail")
	}

	t.Setenv("AGINEX_SESSION_SAME_SITE", "lax")
	t.Setenv("AGINEX_HTTP_MAX_BODY_BYTES", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid body limit to fail")
	}

	t.Setenv("AGINEX_HTTP_MAX_BODY_BYTES", "1024")
	t.Setenv("AGINEX_RATE_LIMIT_LOGIN_LIMIT", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid login rate limit to fail")
	}
}

func TestLoadExplicitRateLimits(t *testing.T) {
	t.Setenv("AGINEX_RATE_LIMIT_LOGIN_LIMIT", "7")
	t.Setenv("AGINEX_RATE_LIMIT_LOGIN_WINDOW", "2m")
	t.Setenv("AGINEX_RATE_LIMIT_UPLOAD_LIMIT", "45")
	t.Setenv("AGINEX_RATE_LIMIT_UPLOAD_WINDOW", "30s")
	t.Setenv("AGINEX_RATE_LIMIT_SENSITIVE_LIMIT", "90")
	t.Setenv("AGINEX_RATE_LIMIT_SENSITIVE_WINDOW", "3m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.LoginLimit != 7 ||
		cfg.RateLimit.LoginWindow != 2*time.Minute ||
		cfg.RateLimit.UploadLimit != 45 ||
		cfg.RateLimit.UploadWindow != 30*time.Second ||
		cfg.RateLimit.SensitiveLimit != 90 ||
		cfg.RateLimit.SensitiveWindow != 3*time.Minute {
		t.Fatalf("rate limits = %#v", cfg.RateLimit)
	}
}

func TestLoadRejectsInvalidJobProviderCombination(t *testing.T) {
	t.Setenv("AGINEX_JOBS_DRIVER", "postgres")
	t.Setenv("AGINEX_DATABASE_DRIVER", "sqlite")
	if _, err := Load(); err == nil {
		t.Fatal("expected PostgreSQL jobs with SQLite database to fail")
	}

	t.Setenv("AGINEX_JOBS_DRIVER", "memory")
	if _, err := Load(); err == nil {
		t.Fatal("expected unsupported jobs driver to fail")
	}
}

func TestValidateRejectsProgrammaticProductionBypass(t *testing.T) {
	cfg := WithDefaults(Config{
		Environment: "production",
		Database:    Database{Driver: "postgres"},
		Jobs:        Jobs{Driver: "postgres"},
		Session: Session{
			Secret:   "short",
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		},
		HTTP: HTTP{PublicURL: "https://api.example.com"},
		Storage: Storage{
			Driver: "local",
		},
		WebOrigins: []string{"https://admin.example.com"},
	})
	if err := Validate(cfg); err == nil {
		t.Fatal("programmatic production configuration bypassed security checks")
	}

	cfg.Environment = "prodution"
	if err := Validate(cfg); err == nil ||
		!strings.Contains(err.Error(), "unsupported AGINEX_ENV") {
		t.Fatalf("unknown environment error = %v", err)
	}
}

func TestLoadExplicitIdempotencyConfiguration(t *testing.T) {
	t.Setenv("AGINEX_IDEMPOTENCY_DRIVER", "disabled")
	t.Setenv("AGINEX_IDEMPOTENCY_LEASE_DURATION", "45s")
	t.Setenv("AGINEX_IDEMPOTENCY_TTL", "72h")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Idempotency.Driver != "disabled" ||
		cfg.Idempotency.LeaseDuration != 45*time.Second ||
		cfg.Idempotency.TTL != 72*time.Hour {
		t.Fatalf("idempotency = %#v", cfg.Idempotency)
	}

	t.Setenv("AGINEX_IDEMPOTENCY_DRIVER", "memory")
	if _, err := Load(); err == nil {
		t.Fatal("expected unsupported idempotency driver to fail")
	}
}
