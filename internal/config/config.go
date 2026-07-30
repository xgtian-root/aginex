package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment string
	HTTP        HTTP
	Database    Database
	Session     Session
	Bootstrap   Bootstrap
	Storage     Storage
	WebOrigin   string
}

type HTTP struct {
	Address   string
	PublicURL string
}

type Database struct {
	Driver string
	DSN    string
}

type Session struct {
	CookieName string
	Secret     string
	Secure     bool
	TTL        time.Duration
}

type Bootstrap struct {
	AdminEmail    string
	AdminPassword string
}

type Storage struct {
	Driver          string
	LocalRoot       string
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
}

func Load() (Config, error) {
	sessionTTL, err := time.ParseDuration(value("AGINEX_SESSION_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("AGINEX_SESSION_TTL: %w", err)
	}

	secureCookie, err := strconv.ParseBool(value("AGINEX_SESSION_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("AGINEX_SESSION_SECURE: %w", err)
	}

	cfg := Config{
		Environment: value("AGINEX_ENV", "development"),
		HTTP: HTTP{
			Address:   value("AGINEX_HTTP_ADDRESS", ":8080"),
			PublicURL: value("AGINEX_API_PUBLIC_URL", "http://localhost:8080"),
		},
		Database: Database{
			Driver: strings.ToLower(value("AGINEX_DATABASE_DRIVER", "sqlite")),
			DSN:    value("AGINEX_DATABASE_DSN", "data/aginex.db"),
		},
		Session: Session{
			CookieName: value("AGINEX_SESSION_COOKIE", "aginex_session"),
			Secret:     os.Getenv("AGINEX_SESSION_SECRET"),
			Secure:     secureCookie,
			TTL:        sessionTTL,
		},
		Bootstrap: Bootstrap{
			AdminEmail:    strings.TrimSpace(strings.ToLower(os.Getenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL"))),
			AdminPassword: os.Getenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD"),
		},
		Storage: Storage{
			Driver:          strings.ToLower(value("AGINEX_STORAGE_DRIVER", "local")),
			LocalRoot:       value("AGINEX_STORAGE_LOCAL_ROOT", "data/uploads"),
			Bucket:          os.Getenv("AGINEX_STORAGE_BUCKET"),
			Region:          os.Getenv("AGINEX_STORAGE_REGION"),
			Endpoint:        os.Getenv("AGINEX_STORAGE_ENDPOINT"),
			AccessKeyID:     os.Getenv("AGINEX_STORAGE_ACCESS_KEY_ID"),
			AccessKeySecret: os.Getenv("AGINEX_STORAGE_ACCESS_KEY_SECRET"),
		},
		WebOrigin: value("AGINEX_WEB_ORIGIN", "http://localhost:3000"),
	}

	switch cfg.Database.Driver {
	case "sqlite", "postgres", "mysql":
	default:
		return Config{}, fmt.Errorf("unsupported database driver %q", cfg.Database.Driver)
	}
	switch cfg.Storage.Driver {
	case "local", "s3", "oss":
	default:
		return Config{}, fmt.Errorf("unsupported storage driver %q", cfg.Storage.Driver)
	}
	if cfg.Environment == "production" && len(cfg.Session.Secret) < 32 {
		return Config{}, fmt.Errorf("AGINEX_SESSION_SECRET must contain at least 32 bytes in production")
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
