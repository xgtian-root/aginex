package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/database"
)

func TestLoginAndProductLifecycle(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		HTTP:        config.HTTP{Address: ":0"},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	loginBody := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}

	createBody := []byte(`{"name":"Agent Desk","sku":"AGENT-DESK","priceCents":129900,"status":"active"}`)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/products", bytes.NewReader(createBody))
	create.Header.Set("Content-Type", "application/json")
	create.AddCookie(cookies[0])
	createRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRecorder.Code, createRecorder.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["sku"] != "AGENT-DESK" {
		t.Fatalf("created sku = %#v", created["sku"])
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	list.AddCookie(cookies[0])
	listRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body.String())
	}

	audit := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil)
	audit.AddCookie(cookies[0])
	auditRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(auditRecorder, audit)
	if auditRecorder.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", auditRecorder.Code, auditRecorder.Body.String())
	}
}

func TestProtectedEndpointRequiresSession(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session:   config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("content-type = %q", contentType)
	}
}

func TestApplicationCanRestartAgainstMigratedDatabase(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Bootstrap: config.Bootstrap{
			AdminEmail: "admin@example.com", AdminPassword: "correct horse battery staple",
		},
		Storage: config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, db); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, db); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
}

func TestLocalImageUploadLifecycle(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		HTTP:        config.HTTP{PublicURL: "http://aginex.test"},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Bootstrap: config.Bootstrap{
			AdminEmail: "admin@example.com", AdminPassword: "correct horse battery staple",
		},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, server)

	image := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	intentBody, _ := json.Marshal(map[string]any{
		"filename": "avatar.png", "contentType": "image/png", "size": len(image), "visibility": "private",
	})
	intent := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload-intents", bytes.NewReader(intentBody))
	intent.Header.Set("Content-Type", "application/json")
	intent.AddCookie(cookie)
	intentRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(intentRecorder, intent)
	if intentRecorder.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", intentRecorder.Code, intentRecorder.Body.String())
	}
	var prepared struct {
		File struct {
			ID string `json:"id"`
		} `json:"file"`
		Upload struct {
			URL string `json:"url"`
		} `json:"upload"`
	}
	if err := json.Unmarshal(intentRecorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	uploadPath := strings.TrimPrefix(prepared.Upload.URL, cfg.HTTP.PublicURL)
	upload := httptest.NewRequest(http.MethodPut, uploadPath, bytes.NewReader(image))
	upload.Header.Set("Content-Type", "image/png")
	upload.AddCookie(cookie)
	uploadRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	confirm := httptest.NewRequest(http.MethodPost, "/api/v1/files/"+prepared.File.ID+"/confirm", nil)
	confirm.AddCookie(cookie)
	confirmRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmRecorder, confirm)
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
}

func loginCookie(t *testing.T, server *App) *http.Cookie {
	t.Helper()
	body := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()[0]
}
