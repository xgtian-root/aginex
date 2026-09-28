package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
)

const testCaptchaAnswer = "A2B3"
const testCSRFToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// Only test code can seed known answers. Production always uses cryptographic
// randomness and never exposes the answer through its API.
func testCaptchaDigest(server *App, purpose, id, value string) string {
	mac := hmac.New(sha256.New, []byte(server.cfg.Session.Secret))
	mac.Write([]byte("aginex:login-captcha:" + purpose + "\x00" + id + "\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func testCaptchaInput(t *testing.T, server *App, input LoginRequest) LoginRequest {
	t.Helper()
	input.CaptchaID = uuid.NewString()
	input.CaptchaCode = testCaptchaAnswer
	err := server.db.Table("login_captchas").Create(map[string]any{
		"id":           input.CaptchaID,
		"answer_hash":  testCaptchaDigest(server, "answer", input.CaptchaID, testCaptchaAnswer),
		"binding_hash": testCaptchaDigest(server, "browser", input.CaptchaID, testCSRFToken),
		"expires_at":   time.Now().UTC().Add(5 * time.Minute), "consumed": false,
	}).Error
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func addTestLoginCaptcha(t *testing.T, server *App, request *http.Request) {
	t.Helper()
	if request.URL.Path != "/api/v1/auth/login" {
		return
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(body, &input); err != nil {
		t.Fatal(err)
	}
	challenge := testCaptchaInput(t, server, LoginRequest{})
	if err := server.db.Table("login_captchas").Where("id = ?", challenge.CaptchaID).Update("binding_hash", testCaptchaDigest(server, "browser", challenge.CaptchaID, request.Header.Get("X-CSRF-Token"))).Error; err != nil {
		t.Fatal(err)
	}
	input["captchaId"], input["captchaCode"] = challenge.CaptchaID, challenge.CaptchaCode
	body, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
}

func captchaLogin(t *testing.T, server *App, input LoginRequest, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "aginex_csrf", Value: token})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	return response
}
func requestCaptcha(server *App) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/captcha", nil)
	addTestCSRF(req)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	return response
}

func TestLoginCaptchaContract(t *testing.T) {
	server := newCaptchaTestApp(t)
	input := LoginRequest{Email: "admin@example.com", Password: "correct horse battery staple"}
	t.Run("missing", func(t *testing.T) {
		assertProblemCode(t, captchaLogin(t, server, input, testCSRFToken), 400, "REQUEST_INVALID")
	})
	t.Run("wrong answer is consumed", func(t *testing.T) {
		request := testCaptchaInput(t, server, input)
		request.CaptchaCode = "ZZZZ"
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 400, "CAPTCHA_INVALID")
		request.CaptchaCode = testCaptchaAnswer
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 400, "CAPTCHA_INVALID")
	})
	t.Run("expired", func(t *testing.T) {
		request := testCaptchaInput(t, server, input)
		if err := server.db.Table("login_captchas").Where("id = ?", request.CaptchaID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 400, "CAPTCHA_INVALID")
	})
	t.Run("browser binding", func(t *testing.T) {
		request := testCaptchaInput(t, server, input)
		assertProblemCode(t, captchaLogin(t, server, request, "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"), 400, "CAPTCHA_INVALID")
	})
	t.Run("password failure keeps captcha consumed", func(t *testing.T) {
		request := testCaptchaInput(t, server, input)
		request.Password = "incorrect"
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 401, "AUTHENTICATION_REQUIRED")
		request.Password = input.Password
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 400, "CAPTCHA_INVALID")
	})
	var count int64
	if err := server.db.Model(&domain.Session{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected requests created %d sessions", count)
	}
	t.Run("case insensitive and single use", func(t *testing.T) {
		request := testCaptchaInput(t, server, input)
		request.CaptchaCode = " a2b3 "
		response := captchaLogin(t, server, request, testCSRFToken)
		if response.Code != 200 {
			t.Fatalf("login: %d %s", response.Code, response.Body.String())
		}
		assertProblemCode(t, captchaLogin(t, server, request, testCSRFToken), 400, "CAPTCHA_INVALID")
	})
}

func newCaptchaTestApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Config{Environment: "test", Database: config.Database{Driver: "sqlite", DSN: t.TempDir() + "/captcha.db"},
		Session:   config.Session{Secret: "test-only-captcha-secret"},
		RateLimit: config.RateLimit{LoginLimit: 100},
		Bootstrap: config.Bootstrap{AdminEmail: "admin@example.com", AdminPassword: "correct horse battery staple"},
		Storage:   config.Storage{Driver: "local", LocalRoot: t.TempDir()}, WebOrigin: "http://localhost:3000"}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCaptchaIssuanceRequiresCSRFAndHasIndependentRateLimit(t *testing.T) {
	server := newCaptchaTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/captcha", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	assertProblemCode(t, response, 403, "CSRF_FORBIDDEN")
	for i := 0; i < 20; i++ {
		response = requestCaptcha(server)
		if response.Code != 200 {
			t.Fatalf("issue %d: %d %s", i, response.Code, response.Body.String())
		}
		var challenge CaptchaResponse
		if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(challenge.Image, "data:image/png;base64,") || challenge.CaptchaID == "" || time.Until(challenge.ExpiresAt) < 4*time.Minute || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invalid challenge response")
		}
	}
	response = requestCaptcha(server)
	assertProblemCode(t, response, 429, "RATE_LIMITED")
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("missing retry header")
	}
	// Captcha issuance must not consume the login IP bucket.
	input := testCaptchaInput(t, server, LoginRequest{Email: "admin@example.com", Password: "correct horse battery staple"})
	if response := captchaLogin(t, server, input, testCSRFToken); response.Code != 200 {
		t.Fatalf("login after image refreshes: %d %s", response.Code, response.Body.String())
	}
}

func TestCaptchaAuditAndDatabaseFailuresFailClosed(t *testing.T) {
	t.Run("audit", func(t *testing.T) {
		server := newCaptchaTestApp(t)
		input := testCaptchaInput(t, server, LoginRequest{Email: "admin@example.com", Password: "correct horse battery staple"})
		if err := server.db.Exec("DROP TABLE audit_logs").Error; err != nil {
			t.Fatal(err)
		}
		assertProblemCode(t, requestCaptcha(server), 503, "CAPTCHA_UNAVAILABLE")
		assertProblemCode(t, captchaLogin(t, server, input, testCSRFToken), 503, "CAPTCHA_UNAVAILABLE")
		var count int64
		if err := server.db.Table("login_captchas").Where("consumed = ?", true).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("audit failure left consumed challenge")
		}
		if err := server.db.Model(&domain.Session{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("audit failure created session")
		}
	})
	t.Run("missing captcha storage", func(t *testing.T) {
		server := newCaptchaTestApp(t)
		input := testCaptchaInput(t, server, LoginRequest{Email: "admin@example.com", Password: "correct horse battery staple"})
		if err := server.db.Exec("DROP TABLE login_captchas").Error; err != nil {
			t.Fatal(err)
		}
		assertProblemCode(t, requestCaptcha(server), 503, "CAPTCHA_UNAVAILABLE")
		assertProblemCode(t, captchaLogin(t, server, input, testCSRFToken), 503, "CAPTCHA_UNAVAILABLE")
	})
}

func TestCaptchaCanBeIssuedAndConsumedAcrossApps(t *testing.T) {
	server := newCaptchaTestApp(t)
	other, err := New(server.cfg, server.db)
	if err != nil {
		t.Fatal(err)
	}
	response := requestCaptcha(server)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var challenge CaptchaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	// Override only this fixture's answer digest after exercising real issuance.
	if err := server.db.Table("login_captchas").Where("id = ?", challenge.CaptchaID).Update("answer_hash", testCaptchaDigest(server, "answer", challenge.CaptchaID, testCaptchaAnswer)).Error; err != nil {
		t.Fatal(err)
	}
	input := LoginRequest{Email: "admin@example.com", Password: "correct horse battery staple", CaptchaID: challenge.CaptchaID, CaptchaCode: testCaptchaAnswer}
	if response := captchaLogin(t, other, input, testCSRFToken); response.Code != 200 {
		t.Fatalf("cross-app login: %d %s", response.Code, response.Body.String())
	}
	assertProblemCode(t, captchaLogin(t, server, input, testCSRFToken), 400, "CAPTCHA_INVALID")
}
