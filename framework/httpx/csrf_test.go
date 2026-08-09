package httpx

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerateCSRFTokenAndCompare(t *testing.T) {
	first, err := GenerateCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(decoded) != CSRFTokenBytes {
		t.Fatalf("token bytes = %d, want %d", len(decoded), CSRFTokenBytes)
	}
	if first == second {
		t.Fatal("two generated CSRF tokens are equal")
	}
	if !EqualCSRFToken(first, first) {
		t.Fatal("equal CSRF tokens did not compare equal")
	}
	if EqualCSRFToken(first, second) || EqualCSRFToken(first, "") || EqualCSRFToken(first, "invalid") {
		t.Fatal("different or malformed CSRF tokens compared equal")
	}
}

func TestReuseOrGenerateCSRFTokenKeepsValidCookieStable(t *testing.T) {
	existing, err := GenerateCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: existing})

	reused, err := ReuseOrGenerateCSRFToken(request, "csrf_token")
	if err != nil {
		t.Fatal(err)
	}
	if reused != existing {
		t.Fatalf("token rotated across tabs: got %q, want existing token", reused)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: "invalid"})
	replacement, err := ReuseOrGenerateCSRFToken(request, "csrf_token")
	if err != nil {
		t.Fatal(err)
	}
	if replacement == "invalid" || !EqualCSRFToken(replacement, replacement) {
		t.Fatalf("invalid cookie was not replaced with a valid token")
	}
}

func TestCSRFMiddlewareAuthenticationRules(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewCSRF(CSRFConfig{
		TokenCookieName:    "csrf_token",
		HeaderName:         "X-CSRF-Token",
		SessionCookieNames: []string{"aginex_session"},
		AllowedOrigins:     []string{"https://admin.example"},
		AllowBearer:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		cookies    []*http.Cookie
		wantStatus int
	}{
		{
			name:       "safe method bypasses CSRF",
			method:     http.MethodGet,
			cookies:    []*http.Cookie{{Name: "aginex_session", Value: "session"}},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "cookie request accepts matching token and origin",
			method: http.MethodPost,
			headers: map[string]string{
				"Origin":       "https://admin.example",
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: token},
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "referer is an allowed origin fallback",
			method: http.MethodPost,
			headers: map[string]string{
				"Referer":      "https://admin.example/settings/profile",
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: token},
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "mismatched token is rejected",
			method: http.MethodPost,
			headers: map[string]string{
				"Origin":       "https://admin.example",
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: "different"},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "missing source is rejected",
			method: http.MethodPost,
			headers: map[string]string{
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: token},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "disallowed origin is rejected",
			method: http.MethodPost,
			headers: map[string]string{
				"Origin":       "https://evil.example",
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: token},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "both source headers must be allowed",
			method: http.MethodPost,
			headers: map[string]string{
				"Origin":       "https://admin.example",
				"Referer":      "https://evil.example/attack",
				"X-CSRF-Token": token,
			},
			cookies: []*http.Cookie{
				{Name: "aginex_session", Value: "session"},
				{Name: "csrf_token", Value: token},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "explicit bearer without session cookie is exempt",
			method: http.MethodPost,
			headers: map[string]string{
				"Authorization": "Bearer access-token",
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "bearer request still rejects supplied evil origin",
			method: http.MethodPost,
			headers: map[string]string{
				"Authorization": "Bearer access-token",
				"Origin":        "https://evil.example",
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "session cookie takes precedence over bearer",
			method: http.MethodPost,
			headers: map[string]string{
				"Authorization": "Bearer access-token",
			},
			cookies:    []*http.Cookie{{Name: "aginex_session", Value: "session"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "no recognized credentials fails closed",
			method:     http.MethodPost,
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "malformed bearer fails closed",
			method: http.MethodPost,
			headers: map[string]string{
				"Authorization": "Bearer",
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveWithMiddleware(t, middleware, test.method, test.headers, test.cookies)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestCSRFMiddlewareCanProtectExplicitUnauthenticatedRoutes(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewCSRF(CSRFConfig{
		TokenCookieName:      "csrf_token",
		HeaderName:           "X-CSRF-Token",
		SessionCookieNames:   []string{"aginex_session"},
		AllowedOrigins:       []string{"https://admin.example"},
		AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := serveWithMiddleware(
		t,
		middleware,
		http.MethodPost,
		map[string]string{"Origin": "https://admin.example", "X-CSRF-Token": token},
		[]*http.Cookie{{Name: "csrf_token", Value: token}},
	)
	if valid.Code != http.StatusNoContent {
		t.Fatalf("valid unauthenticated status = %d, body = %s", valid.Code, valid.Body.String())
	}

	missingToken := serveWithMiddleware(
		t,
		middleware,
		http.MethodPost,
		map[string]string{"Origin": "https://admin.example"},
		nil,
	)
	if missingToken.Code != http.StatusForbidden {
		t.Fatalf("missing-token status = %d, want %d", missingToken.Code, http.StatusForbidden)
	}
}

func serveWithMiddleware(
	t *testing.T,
	middleware gin.HandlerFunc,
	method string,
	headers map[string]string,
	cookies []*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.Any("/resource", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(method, "/resource", nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
