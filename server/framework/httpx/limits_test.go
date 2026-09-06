package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestLimitsRejectKnownOversizedBody(t *testing.T) {
	middleware, err := NewRequestLimits(RequestLimits{
		MaxBodyBytes:   4,
		MaxHeaderBytes: 128,
		MaxHeaderCount: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	router := gin.New()
	router.Use(middleware)
	router.POST("/", func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Fatal("handler ran for a known oversized request")
	}
}

func TestRequestLimitsWrapStreamingBody(t *testing.T) {
	middleware, err := NewRequestLimits(RequestLimits{
		MaxBodyBytes:   4,
		MaxHeaderBytes: 128,
		MaxHeaderCount: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(middleware)
	router.POST("/", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			var tooLarge *http.MaxBytesError
			if !errors.As(err, &tooLarge) {
				t.Fatalf("read body error = %v, want MaxBytesError", err)
			}
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345"))
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestRequestLimitsRejectOversizedHeaders(t *testing.T) {
	middleware, err := NewRequestLimits(RequestLimits{
		MaxBodyBytes:   1024,
		MaxHeaderBytes: 16,
		MaxHeaderCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(middleware)
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Large", strings.Repeat("a", 32))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestHeaderFieldsTooLarge)
	}
}

func TestRequestLimitsRequirePositiveBounds(t *testing.T) {
	for _, limits := range []RequestLimits{
		{},
		{MaxBodyBytes: 1, MaxHeaderBytes: 1},
		{MaxBodyBytes: 1, MaxHeaderCount: 1},
	} {
		if _, err := NewRequestLimits(limits); err == nil {
			t.Fatalf("limits %#v unexpectedly passed validation", limits)
		}
	}
}
