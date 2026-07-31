package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	frameworkidempotency "github.com/xgtian-root/aginex/framework/idempotency"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/database"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"gorm.io/gorm"
)

func TestNewRejectsUnsafeHTTPProtocolBeforeDatabaseAccess(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*config.Config)
		match  string
	}{
		{
			name: "all-address IPv4 trusted proxy",
			mutate: func(cfg *config.Config) {
				cfg.HTTP.TrustedProxies = []string{"0.0.0.0/0"}
			},
			match: "AGINEX_TRUSTED_PROXIES",
		},
		{
			name: "all-address IPv6 trusted proxy",
			mutate: func(cfg *config.Config) {
				cfg.HTTP.TrustedProxies = []string{"::/0"}
			},
			match: "AGINEX_TRUSTED_PROXIES",
		},
		{
			name: "custom CSRF header",
			mutate: func(cfg *config.Config) {
				cfg.Session.CSRFHeader = "X-Custom-CSRF"
			},
			match: "published API contract",
		},
		{
			name: "custom session cookie",
			mutate: func(cfg *config.Config) {
				cfg.Session.CookieName = "custom_session"
			},
			match: "published API contract",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.WithDefaults(config.Config{
				Environment: "test",
				Database: config.Database{
					Driver: "sqlite",
					DSN:    "unused.db",
				},
				Storage: config.Storage{Driver: "local"},
			})
			test.mutate(&cfg)
			if _, err := New(cfg, nil); err == nil ||
				!strings.Contains(err.Error(), test.match) {
				t.Fatalf("New error = %v, want %q", err, test.match)
			}
		})
	}
}

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
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	loginBody := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	addTestCSRF(login)
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
	addTestCSRF(create)
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

func TestProductWriteRollsBackWhenAuditInsertFails(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		HTTP:        config.HTTP{Address: ":0"},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, server)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"name":"Atomic Desk","sku":"ATOMIC-DESK","priceCents":100,"status":"active"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/products", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code < 500 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var productCount int64
	if err := db.Table("products").Where("sku = ?", "ATOMIC-DESK").Count(&productCount).Error; err != nil {
		t.Fatal(err)
	}
	if productCount != 0 {
		t.Fatalf("products = %d, want 0 after audit failure", productCount)
	}
}

func TestLoginRollsBackSessionWhenAuditInsertFails(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code < 500 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var sessionCount int64
	if err := db.Table("sessions").Count(&sessionCount).Error; err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("sessions = %d, want 0 after audit failure", sessionCount)
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
	db := openMigratedDatabase(t, cfg.Database)
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

func TestCSRFEndpointProtectsCookieWritesAndRejectsCrossOriginRequests(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	loginBody := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)

	missingToken := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	missingToken.Header.Set("Content-Type", "application/json")
	missingToken.Header.Set("Origin", cfg.WebOrigin)
	missingRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRecorder, missingToken)
	assertProblemCode(t, missingRecorder, http.StatusForbidden, "CSRF_FORBIDDEN")

	csrfRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	csrfRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(csrfRecorder, csrfRequest)
	if csrfRecorder.Code != http.StatusOK {
		t.Fatalf("csrf status = %d, body = %s", csrfRecorder.Code, csrfRecorder.Body.String())
	}
	var csrf CSRFTokenResponse
	if err := json.Unmarshal(csrfRecorder.Body.Bytes(), &csrf); err != nil {
		t.Fatal(err)
	}
	var csrfCookie *http.Cookie
	for _, cookie := range csrfRecorder.Result().Cookies() {
		if cookie.Name == "aginex_csrf" {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil || csrfCookie.Value != csrf.Token || csrfCookie.HttpOnly {
		t.Fatalf("csrf cookie = %#v, response = %#v", csrfCookie, csrf)
	}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set(csrf.HeaderName, csrf.Token)
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	crossOrigin.AddCookie(csrfCookie)
	crossOriginRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossOriginRecorder, crossOrigin)
	assertProblemCode(t, crossOriginRecorder, http.StatusForbidden, "CORS_FORBIDDEN")

	valid := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set(csrf.HeaderName, csrf.Token)
	valid.Header.Set("Origin", cfg.WebOrigin)
	valid.Header.Set("X-Forwarded-For", "203.0.113.99")
	valid.RemoteAddr = "192.0.2.50:43123"
	valid.AddCookie(csrfCookie)
	validRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusOK {
		t.Fatalf("valid login status = %d, body = %s", validRecorder.Code, validRecorder.Body.String())
	}
	var session domain.Session
	if err := db.Order("created_at DESC").First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.IPAddress != "192.0.2.50" {
		t.Fatalf("session IP = %q, want unproxied remote address", session.IPAddress)
	}
}

func TestRequestLimitsRejectOversizedBodyBeforeHandler(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		HTTP: config.HTTP{
			MaxBodyBytes:   32,
			MaxHeaderBytes: 1024,
			MaxHeaderCount: 20,
		},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session:   config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Storage:   config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"email":"admin@example.com","password":"this body is intentionally too large"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	assertProblemCode(t, recorder, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE")
}

func TestRequestIDRejectsMalformedClientValues(t *testing.T) {
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
	db := openMigratedDatabase(t, cfg.Database)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	request.Header.Set("X-Request-ID", strings.Repeat("x", 65)+"\nmalicious")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	got := recorder.Header().Get("X-Request-ID")
	if got == "" || len(got) > 64 || strings.ContainsAny(got, "\r\n ") {
		t.Fatalf("sanitized request ID = %q", got)
	}
}

func TestRequestContextNormalizesTraceParent(t *testing.T) {
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
	db := openMigratedDatabase(t, cfg.Database)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}

	valid := "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01"
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	request.Header.Set("traceparent", valid)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	got := recorder.Header().Get("traceparent")
	if len(got) != 55 ||
		got[3:35] != strings.ToLower(valid)[3:35] ||
		got[36:52] == strings.ToLower(valid)[36:52] {
		t.Fatalf("server child traceparent = %q", got)
	}

	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	malformed.Header.Set("traceparent", strings.Repeat("f", 512))
	malformedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(malformedRecorder, malformed)
	got = malformedRecorder.Header().Get("traceparent")
	if len(got) != 55 || got == strings.Repeat("f", 512) {
		t.Fatalf("replacement traceparent = %q", got)
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
	db := openMigratedDatabase(t, cfg.Database)
	if _, err := New(cfg, db); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, db); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
}

func TestApplicationRejectsDatabaseWithPendingMigrations(t *testing.T) {
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
		Storage: config.Storage{Driver: "local", LocalRoot: filepath.Join(t.TempDir(), "uploads")},
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := New(cfg, db); !errors.Is(err, migrate.ErrSchemaNotCurrent) {
		t.Fatalf("New error = %v, want ErrSchemaNotCurrent", err)
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
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, server)

	image := validPNG(t)
	intentBody, _ := json.Marshal(map[string]any{
		"filename": "avatar.png", "contentType": "image/png", "size": len(image), "visibility": "private",
	})
	intent := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload-intents", bytes.NewReader(intentBody))
	intent.Header.Set("Content-Type", "application/json")
	intent.AddCookie(cookie)
	addTestCSRF(intent)
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
	addTestCSRF(upload)
	uploadRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	confirm := httptest.NewRequest(http.MethodPost, "/api/v1/files/"+prepared.File.ID+"/confirm", nil)
	confirm.AddCookie(cookie)
	addTestCSRF(confirm)
	confirmRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmRecorder, confirm)
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
	var confirmed FileResponse
	if err := json.Unmarshal(confirmRecorder.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if len(confirmed.SHA256) != 64 || confirmed.Width != 2 || confirmed.Height != 2 {
		t.Fatalf("verified metadata = %#v", confirmed)
	}
}

func loginCookie(t *testing.T, server *App) *http.Cookie {
	t.Helper()
	body := []byte(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()[0]
}

func addTestCSRF(request *http.Request) {
	const token = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	request.AddCookie(&http.Cookie{Name: "aginex_csrf", Value: token})
	request.Header.Set("X-CSRF-Token", token)
	request.Header.Set("Origin", "http://localhost:3000")
}

func assertProblemCode(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, status, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("content type = %q", contentType)
	}
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code || problem.RequestID == "" {
		t.Fatalf("problem = %#v, want code %q and request ID", problem, code)
	}
}

func openMigratedDatabase(t *testing.T, cfg config.Database) *gorm.DB {
	t.Helper()
	db, err := database.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(sqlDB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(
		t.Context(),
		sqlDB,
		cfg.Driver,
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}
	provider, err := frameworkidempotency.NewMigrationProvider(sqlDB, cfg.Driver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

func bootstrapTestData(t *testing.T, db *gorm.DB, cfg config.Bootstrap) {
	t.Helper()
	if err := Bootstrap(t.Context(), db, cfg); err != nil {
		t.Fatal(err)
	}
}
