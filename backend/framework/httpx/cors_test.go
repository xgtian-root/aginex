package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSCredentialsEchoOnlyAllowedOrigins(t *testing.T) {
	middleware, err := NewCORS(CORSConfig{
		AllowedOrigins:   []string{"https://admin.example"},
		AllowCredentials: true,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost},
		AllowedHeaders:   []string{"Content-Type", "X-CSRF-Token"},
	})
	if err != nil {
		t.Fatal(err)
	}

	allowed := corsRequest(middleware, http.MethodGet, "https://admin.example")
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed status = %d", allowed.Code)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example" {
		t.Fatalf("allow-origin = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow-credentials = %q", got)
	}
	if strings.Contains(allowed.Header().Get("Access-Control-Allow-Origin"), "*") {
		t.Fatal("credentialed CORS emitted a wildcard origin")
	}

	disallowed := corsRequest(middleware, http.MethodGet, "https://evil.example")
	if disallowed.Code != http.StatusForbidden {
		t.Fatalf("disallowed status = %d, want %d", disallowed.Code, http.StatusForbidden)
	}
	if got := disallowed.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed allow-origin = %q", got)
	}
}

func TestCORSPreflightUsesConfiguredMethodsAndHeaders(t *testing.T) {
	middleware, err := NewCORS(CORSConfig{
		AllowedOrigins: []string{"https://admin.example"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost},
		AllowedHeaders: []string{"Content-Type", "X-CSRF-Token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := corsRequest(middleware, http.MethodOptions, "https://admin.example")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("allow-methods = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-CSRF-Token" {
		t.Fatalf("allow-headers = %q", got)
	}
}

func TestCORSRejectsWildcardConfiguration(t *testing.T) {
	for _, credentials := range []bool{false, true} {
		_, err := NewCORS(CORSConfig{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: credentials,
		})
		if err == nil {
			t.Fatalf("credentials=%t: expected wildcard configuration to fail", credentials)
		}
	}
}

func corsRequest(middleware gin.HandlerFunc, method, origin string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware)
	router.Any("/resource", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(method, "/resource", nil)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
