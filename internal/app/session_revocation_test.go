package app

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"gorm.io/gorm"
)

func TestRevokeAllSessionsIsProtectedScopedAuditedAndClearsCookie(t *testing.T) {
	server, db := newSessionRevocationTestApp(t)
	const (
		otherEmail    = "other-sessions@example.com"
		otherPassword = "other user password value"
	)
	createFileUser(t, db, otherEmail, otherPassword, frameworkauthz.ScopeOwn)

	firstAdminCookie := loginCookie(t, server)
	secondAdminCookie := loginCookie(t, server)
	firstOtherCookie := loginCookieAs(t, server, otherEmail, otherPassword)
	secondOtherCookie := loginCookieAs(t, server, otherEmail, otherPassword)

	unauthenticated := serveRequest(
		server,
		nil,
		http.MethodDelete,
		"/api/v1/auth/sessions",
		nil,
		"",
	)
	assertProblemCode(
		t,
		unauthenticated,
		http.StatusUnauthorized,
		"AUTHENTICATION_REQUIRED",
	)

	var admin domain.User
	if err := db.Where("email = ?", "admin@example.com").First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	var other domain.User
	if err := db.Where("email = ?", otherEmail).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	assertAppSessionCount(t, db, admin.ID, 2)
	assertAppSessionCount(t, db, other.ID, 2)

	otherPrincipal, err := server.auth.Authenticate(firstOtherCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if scope, ok := otherPrincipal.Scope("sessions:delete"); !ok || scope != frameworkauthz.ScopeOwn {
		t.Fatalf("ordinary user sessions:delete scope = %q, %v; want own, true", scope, ok)
	}
	response := serveRequest(
		server,
		firstOtherCookie,
		http.MethodDelete,
		"/api/v1/auth/sessions",
		nil,
		"",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("ordinary revoke all status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.Len() != 0 {
		t.Fatalf("revoke all body = %q, want empty", response.Body.String())
	}
	cleared := responseSessionCookie(response, server.cfg.Session.CookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.HttpOnly {
		t.Fatalf("cleared session cookie = %#v", cleared)
	}

	assertAppSessionCount(t, db, other.ID, 0)
	assertAppSessionCount(t, db, admin.ID, 2)
	if _, err := server.auth.Authenticate(secondOtherCookie.Value); err == nil {
		t.Fatal("a second session for the ordinary user remained valid")
	}
	if _, err := server.auth.Authenticate(firstAdminCookie.Value); err != nil {
		t.Fatalf("ordinary user revoked the administrator session: %v", err)
	}
	if _, err := server.auth.Authenticate(secondAdminCookie.Value); err != nil {
		t.Fatalf("ordinary user revoked another administrator session: %v", err)
	}

	adminResponse := serveRequest(
		server,
		firstAdminCookie,
		http.MethodDelete,
		"/api/v1/auth/sessions",
		nil,
		"",
	)
	if adminResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"administrator revoke all status = %d, body = %s",
			adminResponse.Code,
			adminResponse.Body.String(),
		)
	}
	assertAppSessionCount(t, db, admin.ID, 0)
	if _, err := server.auth.Authenticate(secondAdminCookie.Value); err == nil {
		t.Fatal("a second administrator session remained valid")
	}

	var event domain.AuditLog
	if err := db.Where(
		"action = ? AND actor_id = ?",
		"sessions:revoke-all",
		other.ID,
	).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.ActorID == nil || *event.ActorID != other.ID ||
		event.ActorKind != "user" ||
		event.Resource != "session" ||
		event.ResourceID != other.ID ||
		event.Result != "success" ||
		event.Source != "http" {
		t.Fatalf("revoke-all audit event = %#v", event)
	}
	if event.Before["userId"] != other.ID {
		t.Fatalf("audit before = %#v", event.Before)
	}
	if _, ok := event.Before["sessionCount"]; !ok {
		t.Fatalf("audit before omits revoked session count: %#v", event.Before)
	}
}

func TestRevokeAllSessionsRollsBackWhenAuditInsertFails(t *testing.T) {
	server, db := newSessionRevocationTestApp(t)
	firstCookie := loginCookie(t, server)
	secondCookie := loginCookie(t, server)
	var admin domain.User
	if err := db.Where("email = ?", "admin@example.com").First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	response := serveRequest(
		server,
		firstCookie,
		http.MethodDelete,
		"/api/v1/auth/sessions",
		nil,
		"",
	)
	assertProblemCode(t, response, http.StatusInternalServerError, "INTERNAL_ERROR")
	if cookie := responseSessionCookie(response, server.cfg.Session.CookieName); cookie != nil {
		t.Fatalf("failed revocation cleared the session cookie: %#v", cookie)
	}
	assertAppSessionCount(t, db, admin.ID, 2)
	if _, err := server.auth.Authenticate(firstCookie.Value); err != nil {
		t.Fatalf("first session was not rolled back: %v", err)
	}
	if _, err := server.auth.Authenticate(secondCookie.Value); err != nil {
		t.Fatalf("second session was not rolled back: %v", err)
	}
}

func TestOpenAPIRevokeAllSessionsContract(t *testing.T) {
	document := BuildOpenAPI()
	operation := operationAt(
		document,
		module.MethodDelete,
		"/api/v1/auth/sessions",
	)
	if operation == nil {
		t.Fatal("DELETE /api/v1/auth/sessions is missing")
	}
	if operation.OperationID != "revokeAllSessions" {
		t.Fatalf("operation ID = %q, want revokeAllSessions", operation.OperationID)
	}
	if operation.RequestBody != nil {
		t.Fatal("revokeAllSessions must not accept a request body")
	}
	if len(operation.Security) != 1 {
		t.Fatalf("security = %#v", operation.Security)
	}
	if _, ok := operation.Security[0][sessionScheme]; !ok {
		t.Fatalf("security = %#v, want %s", operation.Security, sessionScheme)
	}
	for _, status := range []string{"204", "401", "403", "500", "default"} {
		if operation.Responses[status] == nil {
			t.Fatalf("revokeAllSessions is missing response %s", status)
		}
	}
	for _, status := range []string{"401", "403", "500", "default"} {
		response := operation.Responses[status]
		if response.Content[problemMediaType] == nil ||
			response.Content[problemMediaType].Schema == nil {
			t.Fatalf("response %s lacks a typed problem body", status)
		}
	}
}

func newSessionRevocationTestApp(t *testing.T) (*App, *gorm.DB) {
	t.Helper()
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
	if err := Bootstrap(context.Background(), db, cfg.Bootstrap); err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return server, db
}

func assertAppSessionCount(t *testing.T, db *gorm.DB, userID string, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(&domain.Session{}).
		Where("user_id = ?", userID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("sessions for %s = %d, want %d", userID, count, want)
	}
}

func responseSessionCookie(
	response interface{ Result() *http.Response },
	name string,
) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
