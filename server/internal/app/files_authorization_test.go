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

	"github.com/google/uuid"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/internal/auth"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/password"
	"gorm.io/gorm"
)

func TestFileAuthorizationScopesOwnersAndAllowsAllGrant(t *testing.T) {
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

	const passwordValue = "correct horse battery staple"
	createFileUser(t, db, "user-a@example.com", passwordValue, frameworkauthz.ScopeOwn)
	createFileUser(t, db, "user-b@example.com", passwordValue, frameworkauthz.ScopeOwn)
	adminCookie := loginCookieAs(t, server, cfg.Bootstrap.AdminEmail, cfg.Bootstrap.AdminPassword)
	userACookie := loginCookieAs(t, server, "user-a@example.com", passwordValue)
	userBCookie := loginCookieAs(t, server, "user-b@example.com", passwordValue)

	assertPrincipalScope(t, server, adminCookie, frameworkauthz.ActorKindUser, frameworkauthz.ScopeAll)
	assertPrincipalScope(t, server, userACookie, frameworkauthz.ActorKindUser, frameworkauthz.ScopeOwn)

	image := validPNG(t)
	intentBody, err := json.Marshal(map[string]any{
		"filename": "private-b.png", "contentType": "image/png", "size": len(image), "visibility": "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	intentRecorder := serveRequest(
		server,
		userBCookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		intentBody,
		"application/json",
	)
	if intentRecorder.Code != http.StatusCreated {
		t.Fatalf("B create intent status = %d, body = %s", intentRecorder.Code, intentRecorder.Body.String())
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
	if prepared.Upload.URL == "" {
		t.Fatal("single upload intent omitted upload URL")
	}
	uploadPath := strings.TrimPrefix(prepared.Upload.URL, cfg.HTTP.PublicURL)

	aUpload := serveRequest(server, userACookie, http.MethodPut, uploadPath, image, "application/octet-stream")
	if aUpload.Code != http.StatusNotFound {
		t.Errorf("A upload B intent status = %d, want 404; body = %s", aUpload.Code, aUpload.Body.String())
	}
	aConfirm := serveRequest(
		server,
		userACookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if aConfirm.Code != http.StatusNotFound {
		t.Errorf("A confirm B file status = %d, want 404; body = %s", aConfirm.Code, aConfirm.Body.String())
	}

	bUpload := serveRequest(server, userBCookie, http.MethodPut, uploadPath, image, "application/octet-stream")
	if bUpload.Code != http.StatusNoContent {
		t.Fatalf("B upload status = %d, body = %s", bUpload.Code, bUpload.Body.String())
	}
	bConfirm := serveRequest(
		server,
		userBCookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if bConfirm.Code != http.StatusOK {
		t.Fatalf("B confirm status = %d, body = %s", bConfirm.Code, bConfirm.Body.String())
	}

	aList := serveRequest(server, userACookie, http.MethodGet, "/api/v1/files", nil, "")
	assertFileListTotal(t, "A", aList, 0)
	bList := serveRequest(server, userBCookie, http.MethodGet, "/api/v1/files", nil, "")
	assertFileListTotal(t, "B", bList, 1)

	aURL := serveRequest(
		server,
		userACookie,
		http.MethodGet,
		"/api/v1/files/"+prepared.File.ID+"/url",
		nil,
		"",
	)
	if aURL.Code != http.StatusNotFound {
		t.Errorf("A read B URL status = %d, want 404; body = %s", aURL.Code, aURL.Body.String())
	}
	contentPath := strings.Replace(uploadPath, "/local-upload/", "/local-content/", 1)
	aContent := serveRequest(server, userACookie, http.MethodGet, contentPath, nil, "")
	if aContent.Code != http.StatusNotFound {
		t.Errorf("A read B content status = %d, want 404; body = %s", aContent.Code, aContent.Body.String())
	}

	adminList := serveRequest(server, adminCookie, http.MethodGet, "/api/v1/files", nil, "")
	assertFileListTotal(t, "admin", adminList, 1)
	adminURL := serveRequest(
		server,
		adminCookie,
		http.MethodGet,
		"/api/v1/files/"+prepared.File.ID+"/url",
		nil,
		"",
	)
	if adminURL.Code != http.StatusOK {
		t.Errorf("admin read B URL status = %d, want 200; body = %s", adminURL.Code, adminURL.Body.String())
	}

	aDelete := serveRequest(
		server,
		userACookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	if aDelete.Code != http.StatusNotFound {
		t.Errorf("A delete B file status = %d, want 404; body = %s", aDelete.Code, aDelete.Body.String())
	}
	var remaining int64
	if err := db.Model(&domain.FileObject{}).Where("id = ?", prepared.File.ID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("B file rows after A delete = %d, want 1", remaining)
	}

	server.jobs = &stubTransactionalQueue{}
	adminDelete := serveRequest(
		server,
		adminCookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	if adminDelete.Code != http.StatusAccepted {
		t.Fatalf("admin delete B file status = %d, want 202; body = %s", adminDelete.Code, adminDelete.Body.String())
	}
	if err := db.Model(&domain.FileObject{}).Where("id = ?", prepared.File.ID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("B file rows after admin delete = %d, want 1 pending cleanup", remaining)
	}
	var deleting domain.FileObject
	if err := db.First(&deleting, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if deleting.Status != "deleting" {
		t.Errorf("B file status after admin delete = %q, want deleting", deleting.Status)
	}
}

func createFileUser(
	t *testing.T,
	db *gorm.DB,
	email string,
	passwordValue string,
	scope frameworkauthz.GrantScope,
) {
	t.Helper()
	passwordHash, err := password.Hash(passwordValue)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	user := domain.User{
		ID: uuid.NewString(), Email: email, DisplayName: email,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	identity := domain.UserIdentity{
		ID:             uuid.NewString(),
		UserID:         user.ID,
		Provider:       domain.IdentityProviderPassword,
		Subject:        auth.NormalizePasswordSubject(email),
		CredentialHash: passwordHash,
		Status:         domain.IdentityStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}
	role := domain.Role{
		ID: uuid.NewString(), Name: "files-" + user.ID, Description: "File owner fixture",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	var permissions []domain.Permission
	if err := db.Where("code IN ?", []string{"files:create", "files:read", "files:delete"}).Find(&permissions).Error; err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 3 {
		t.Fatalf("file permissions = %d, want 3", len(permissions))
	}
	for _, permission := range permissions {
		if err := db.Exec(
			"INSERT INTO role_permissions (role_id, permission_id, scope) VALUES (?, ?, ?)",
			role.ID,
			permission.ID,
			scope,
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(
		"INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)",
		user.ID,
		role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
}

func loginCookieAs(t *testing.T, server *App, email, passwordValue string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": passwordValue})
	if err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(server, nil, http.MethodPost, "/api/v1/auth/login", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("login %s status = %d, body = %s", email, recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login %s cookies = %d, want 1", email, len(cookies))
	}
	return cookies[0]
}

func assertPrincipalScope(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	kind frameworkauthz.ActorKind,
	scope frameworkauthz.GrantScope,
) {
	t.Helper()
	principal, err := server.auth.Authenticate(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	actor := principal.Actor()
	if actor.Kind != kind {
		t.Fatalf("actor kind = %q, want %q", actor.Kind, kind)
	}
	got, ok := principal.Scope("files:read")
	if !ok || got != scope {
		t.Fatalf("files:read scope = %q, %v; want %q, true", got, ok, scope)
	}
}

func serveRequest(
	server *App,
	cookie *http.Cookie,
	method string,
	path string,
	body []byte,
	contentType string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		addTestCSRF(request)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func assertFileListTotal(t *testing.T, actor string, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s list status = %d, body = %s", actor, recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []FileResponse `json:"items"`
		Total int            `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != want || len(response.Items) != want {
		t.Errorf(
			"%s list total/items = %d/%d, want %d/%d",
			actor,
			response.Total,
			len(response.Items),
			want,
			want,
		)
	}
}
