package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/framework/ratelimit"
	"github.com/xgtian-root/aginex/internal/config"
)

func TestLoginRateLimitIsSharedAcrossApplicationInstances(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		RateLimit: config.RateLimit{
			LoginLimit:      1,
			LoginWindow:     5 * time.Minute,
			UploadLimit:     10,
			UploadWindow:    time.Minute,
			SensitiveLimit:  10,
			SensitiveWindow: time.Minute,
		},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	first, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	firstAttempt := invalidLoginRequest()
	firstRecorder := httptest.NewRecorder()
	first.Handler().ServeHTTP(firstRecorder, firstAttempt)
	if firstRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("first login status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
	}
	if firstRecorder.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("first remaining = %q", firstRecorder.Header().Get("RateLimit-Remaining"))
	}

	secondAttempt := invalidLoginRequest()
	secondRecorder := httptest.NewRecorder()
	second.Handler().ServeHTTP(secondRecorder, secondAttempt)
	assertProblemCode(t, secondRecorder, http.StatusTooManyRequests, "RATE_LIMITED")
	if secondRecorder.Header().Get("Retry-After") == "" ||
		secondRecorder.Header().Get("RateLimit-Reset") == "" {
		t.Fatalf("rate limit headers = %#v", secondRecorder.Header())
	}

	var hashes []string
	if err := db.Table(ratelimit.TableName).Pluck("key_hash", &hashes).Error; err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 || len(hashes[0]) != 64 || len(hashes[1]) != 64 {
		t.Fatalf("persisted rate keys = %#v", hashes)
	}
	for _, hash := range hashes {
		if strings.Contains(hash, "192.0.2.1") ||
			strings.Contains(hash, "admin@example.com") {
			t.Fatalf("persisted key contains raw client identity: %q", hash)
		}
	}
}

func TestLoginAccountRateLimitAppliesAcrossClientIPs(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		RateLimit: config.RateLimit{
			LoginLimit:      1,
			LoginWindow:     5 * time.Minute,
			UploadLimit:     10,
			UploadWindow:    time.Minute,
			SensitiveLimit:  10,
			SensitiveWindow: time.Minute,
		},
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

	first := invalidLoginRequest()
	first.RemoteAddr = "192.0.2.10:1234"
	firstRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("first login status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
	}

	second := invalidLoginRequest()
	second.RemoteAddr = "198.51.100.20:5678"
	secondRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondRecorder, second)
	assertProblemCode(t, secondRecorder, http.StatusTooManyRequests, "RATE_LIMITED")
}

func invalidLoginRequest() *http.Request {
	body := []byte(`{"email":"admin@example.com","password":"definitely incorrect"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	addTestCSRF(request)
	return request
}
