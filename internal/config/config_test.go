package config

import "testing"

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
}

func TestProductionRequiresSessionSecret(t *testing.T) {
	t.Setenv("AGINEX_ENV", "production")
	t.Setenv("AGINEX_SESSION_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected production secret validation to fail")
	}
}
