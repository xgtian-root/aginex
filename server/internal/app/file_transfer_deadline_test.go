package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/httpx"
)

func TestFileTransferDeadlineCancelsRequestContext(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelParent()
	request := httptest.NewRequest("PUT", "/api/v1/files/local-upload/key", nil).WithContext(parent)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()

	select {
	case <-c.Request.Context().Done():
		if c.Request.Context().Err() != context.DeadlineExceeded {
			t.Fatalf("request context error = %v", c.Request.Context().Err())
		}
	case <-time.After(time.Second):
		t.Fatal("file transfer request context was not cancelled")
	}
}

func TestFileVerificationInterruptionMapsToRequestTimeoutProblem(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/files/upload-sessions/session/complete", nil)

	if !writeFileVerificationInterrupted(c, errors.Join(errors.New("read interrupted"), context.DeadlineExceeded)) {
		t.Fatal("deadline error was not handled")
	}
	if recorder.Code != http.StatusRequestTimeout || recorder.Header().Get("Content-Type") != httpx.ProblemMediaType {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var problem httpx.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != http.StatusRequestTimeout || problem.Instance != c.Request.URL.Path {
		t.Fatalf("problem = %#v", problem)
	}

	ignored := httptest.NewRecorder()
	ignoredContext, _ := gin.CreateTestContext(ignored)
	ignoredContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/files/confirm", nil)
	if writeFileVerificationInterrupted(ignoredContext, errors.New("provider read failed")) {
		t.Fatal("non-context error was handled as a timeout")
	}
}
