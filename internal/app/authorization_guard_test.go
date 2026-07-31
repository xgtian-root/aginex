package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/framework/module"
)

func TestAuthorizationGuardRejectsRedirectBeforeObjectCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/private-id",
		nil,
	)
	writer := newAuthorizationGuardWriter(
		context.Writer,
		request,
		module.RequestAuthorization{},
		module.AuthorizationObject,
	)
	writer.Header().Set("Location", "/leaked/private-id")
	writer.WriteHeader(http.StatusFound)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"redirect status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("unauthorized redirect location leaked: %q", location)
	}
	if !strings.Contains(
		response.Body.String(),
		"AUTHORIZATION_SCOPE_UNUSED",
	) {
		t.Fatalf("authorization problem = %s", response.Body.String())
	}
}

func TestAuthorizationGuardRejectsErrorBodyBeforeObjectCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/private-id",
		nil,
	)
	writer := newAuthorizationGuardWriter(
		context.Writer,
		request,
		module.RequestAuthorization{},
		module.AuthorizationObject,
	)
	writer.Header().Set("X-Signed-URL", "https://secret.example/token")
	writer.WriteHeader(http.StatusNotFound)
	_, _ = writer.Write([]byte(`{"secret":"other-user"}`))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"error status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if leaked := response.Header().Get("X-Signed-URL"); leaked != "" {
		t.Fatalf("unauthorized error header leaked: %q", leaked)
	}
	if strings.Contains(response.Body.String(), "other-user") ||
		!strings.Contains(
			response.Body.String(),
			"AUTHORIZATION_SCOPE_UNUSED",
		) {
		t.Fatalf("unauthorized error body = %s", response.Body.String())
	}
}
