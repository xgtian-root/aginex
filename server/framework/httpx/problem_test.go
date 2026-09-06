package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAbortProblemWritesStableRFCEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/private", func(c *gin.Context) {
		c.Header("X-Request-ID", "request-123")
		AbortProblem(
			c,
			http.StatusForbidden,
			"CSRF_FORBIDDEN",
			"Request denied",
			"CSRF validation failed.",
		)
	})

	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != ProblemMediaType {
		t.Fatalf("content type = %q", got)
	}
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "CSRF_FORBIDDEN" ||
		problem.RequestID != "request-123" ||
		problem.Instance != "/private" {
		t.Fatalf("problem = %#v", problem)
	}
}
