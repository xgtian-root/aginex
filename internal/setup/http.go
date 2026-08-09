package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/internal/buildinfo"
)

type setupHealthResponse struct {
	Status  string    `json:"status"`
	Time    time.Time `json:"time"`
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
}

type csrfTokenResponse struct {
	Token      string `json:"token"`
	HeaderName string `json:"headerName"`
}

func (s *Supervisor) newModeHandler(config Config) (http.Handler, error) {
	router, err := s.newSecureRouter(config)
	if err != nil {
		return nil, err
	}
	router.GET(SystemModePath, func(c *gin.Context) {
		c.JSON(http.StatusOK, SystemModeResponse{Mode: s.Mode()})
	})
	registerNotFound(router)
	return router, nil
}

func (s *Supervisor) newSetupHandler(config Config) (http.Handler, error) {
	router, err := s.newSecureRouter(config)
	if err != nil {
		return nil, err
	}

	live := func(c *gin.Context) {
		c.JSON(http.StatusOK, setupHealthResponse{
			Status:  "ok",
			Time:    s.now().UTC(),
			Version: buildinfo.Version,
			Commit:  buildinfo.Commit,
		})
	}
	ready := func(c *gin.Context) {
		c.JSON(http.StatusOK, setupHealthResponse{
			Status:  "ready",
			Time:    s.now().UTC(),
			Version: buildinfo.Version,
			Commit:  buildinfo.Commit,
		})
	}
	router.GET("/health/live", live)
	router.GET("/health/ready", ready)
	router.GET("/api/v1/health/live", live)
	router.GET("/api/v1/health/ready", ready)
	router.GET("/api/v1/auth/csrf", s.issueCSRFToken(config))
	router.GET(SetupStatusPath, s.getStatus)
	router.POST(SetupDatabaseTestPath, s.requireWriteRateLimit(s.testDatabase))
	router.POST(SetupCompletePath, s.requireWriteRateLimit(s.completeSetup))
	registerNotFound(router)
	return router, nil
}

func (s *Supervisor) newSecureRouter(config Config) (*gin.Engine, error) {
	router := gin.New()
	if err := router.SetTrustedProxies(config.TrustedProxies); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	requestLimits, err := httpx.NewRequestLimits(config.RequestLimits)
	if err != nil {
		return nil, err
	}
	cors, err := httpx.NewCORS(httpx.CORSConfig{
		AllowedOrigins:   config.AllowedOrigins,
		AllowCredentials: true,
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodOptions,
		},
		AllowedHeaders: []string{
			"Content-Type",
			httpx.CSRFHeaderName,
			"X-Request-ID",
		},
		ExposedHeaders: []string{
			"RateLimit-Limit",
			"RateLimit-Remaining",
			"RateLimit-Reset",
			"Retry-After",
			"X-Request-ID",
		},
	})
	if err != nil {
		return nil, err
	}
	csrf, err := httpx.NewCSRF(httpx.CSRFConfig{
		TokenCookieName:      httpx.CSRFCookieName,
		HeaderName:           httpx.CSRFHeaderName,
		SessionCookieNames:   []string{httpx.SessionCookieName},
		AllowedOrigins:       config.AllowedOrigins,
		AllowBearer:          false,
		AllowUnauthenticated: true,
	})
	if err != nil {
		return nil, err
	}

	router.Use(
		requestIDMiddleware(),
		gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			httpx.AbortProblem(
				c,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Request failed",
				"The request could not be processed.",
			)
		}),
		noStoreMiddleware(),
		requestLimits,
		cors,
		csrf,
	)
	return router, nil
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := httpx.RequestID(c.GetHeader("X-Request-ID"))
		c.Request.Header.Set("X-Request-ID", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func noStoreMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func registerNotFound(router *gin.Engine) {
	router.NoRoute(func(c *gin.Context) {
		httpx.WriteProblem(
			c,
			http.StatusNotFound,
			"RESOURCE_NOT_FOUND",
			"Resource not found",
			"The requested resource does not exist.",
		)
	})
	router.NoMethod(func(c *gin.Context) {
		httpx.WriteProblem(
			c,
			http.StatusNotFound,
			"RESOURCE_NOT_FOUND",
			"Resource not found",
			"The requested resource does not exist.",
		)
	})
}

func (s *Supervisor) issueCSRFToken(config Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := httpx.ReuseOrGenerateCSRFToken(
			c.Request,
			httpx.CSRFCookieName,
		)
		if err != nil {
			httpx.WriteProblem(
				c,
				http.StatusInternalServerError,
				"CSRF_TOKEN_UNAVAILABLE",
				"CSRF token unavailable",
				"A request token could not be generated.",
			)
			return
		}
		expiresAt := s.now().Add(config.CSRFTokenTTL)
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     httpx.CSRFCookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   int(config.CSRFTokenTTL.Seconds()),
			Expires:  expiresAt,
			HttpOnly: false,
			Secure:   config.CSRFCookieSecure,
			SameSite: config.CSRFCookieSameSite,
		})
		c.JSON(http.StatusOK, csrfTokenResponse{
			Token:      token,
			HeaderName: httpx.CSRFHeaderName,
		})
	}
}

func (s *Supervisor) getStatus(c *gin.Context) {
	if s.Mode() != ModeSetup {
		writeSetupNotFound(c)
		return
	}
	c.JSON(http.StatusOK, s.Status())
}

func (s *Supervisor) testDatabase(c *gin.Context) {
	if s.Mode() != ModeSetup {
		writeSetupNotFound(c)
		return
	}
	var request SetupDatabaseTestRequest
	if !decodeJSONRequest(c, &request) ||
		!validDatabaseConfig(request.Database) {
		writeInvalidRequest(c)
		return
	}

	ctx, cancel := contextWithTimeout(c, s.databaseTestTimeout)
	defer cancel()
	if err := invokeDatabaseTester(s.tester, ctx, request.Database); err != nil {
		httpx.WriteProblem(
			c,
			http.StatusUnprocessableEntity,
			"SETUP_DATABASE_UNAVAILABLE",
			"Database connection failed",
			"The database configuration could not be verified.",
		)
		return
	}
	c.JSON(http.StatusOK, SetupDatabaseTestResponse{Status: "ok"})
}

func contextWithTimeout(
	c *gin.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), timeout)
}

func (s *Supervisor) completeSetup(c *gin.Context) {
	if s.Mode() != ModeSetup {
		writeSetupNotFound(c)
		return
	}
	var request SetupCompleteRequest
	if !decodeJSONRequest(c, &request) || !validCompleteRequest(request) {
		writeInvalidRequest(c)
		return
	}

	result, generation := s.beginAttempt()
	switch result {
	case beginAttemptInProgress:
		httpx.WriteProblem(
			c,
			http.StatusConflict,
			"SETUP_IN_PROGRESS",
			"Setup is already running",
			"Wait for the current setup attempt to finish.",
		)
		return
	case beginAttemptNotSetup:
		writeSetupNotFound(c)
		return
	}

	metadata := RequestMetadata{
		RequestID: c.Writer.Header().Get("X-Request-ID"),
		IPAddress: trustedClientIP(c.ClientIP()),
	}
	s.startInitialization(request, metadata, generation)
	c.JSON(http.StatusAccepted, SetupAcceptedResponse{
		Status: StatusInitializing,
	})
}

func decodeJSONRequest(c *gin.Context, target any) bool {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.WriteProblem(
			c,
			http.StatusUnsupportedMediaType,
			"UNSUPPORTED_MEDIA_TYPE",
			"Unsupported media type",
			"Use application/json for this request.",
		)
		return false
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.WriteProblem(
				c,
				http.StatusRequestEntityTooLarge,
				"REQUEST_TOO_LARGE",
				"Request body is too large",
				"Reduce the request body size.",
			)
			return false
		}
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false
	}
	return true
}

func validCompleteRequest(request SetupCompleteRequest) bool {
	return validDatabaseConfig(request.Database) &&
		validAdministratorConfig(request.Administrator)
}

func validDatabaseConfig(config DatabaseConfig) bool {
	switch config.Driver {
	case "sqlite", "postgres", "mysql":
	default:
		return false
	}
	return config.DSN != "" &&
		config.DSN == strings.TrimSpace(config.DSN) &&
		len(config.DSN) <= 8192
}

func validAdministratorConfig(config AdministratorConfig) bool {
	if config.Email == "" ||
		config.Email != strings.TrimSpace(config.Email) ||
		len(config.Email) > 320 ||
		len(config.Password) < 12 ||
		len(config.Password) > 1024 ||
		httpx.CredentialLooksInsecure(config.Password) {
		return false
	}
	address, err := mail.ParseAddress(config.Email)
	return err == nil && address.Address == config.Email
}

func writeInvalidRequest(c *gin.Context) {
	// decodeJSONRequest writes media-type and size errors itself. Avoid
	// overwriting those responses while keeping all schema errors generic.
	if c.Writer.Written() {
		return
	}
	httpx.WriteProblem(
		c,
		http.StatusBadRequest,
		"REQUEST_INVALID",
		"Invalid request",
		"The request body is invalid.",
	)
}

func writeSetupNotFound(c *gin.Context) {
	httpx.WriteProblem(
		c,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
		"Resource not found",
		"The requested resource does not exist.",
	)
}

func trustedClientIP(value string) string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

func (s *Supervisor) requireWriteRateLimit(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Setup is reachable before any administrator exists, so its write
		// surface deliberately requires an explicit browser Origin header. The
		// shared CSRF middleware permits an allowlisted Referer fallback for
		// ordinary authenticated application routes; that fallback is too broad
		// for first-installation writes.
		if strings.TrimSpace(c.GetHeader("Origin")) == "" {
			httpx.AbortProblem(
				c,
				http.StatusForbidden,
				"CSRF_FORBIDDEN",
				"Request denied",
				"CSRF token or request origin validation failed.",
			)
			return
		}
		key := c.FullPath() + "|" + trustedClientIP(c.ClientIP())
		decision := s.rateLimiter.consume(key)
		c.Header("RateLimit-Limit", strconv.FormatUint(decision.limit, 10))
		c.Header(
			"RateLimit-Remaining",
			strconv.FormatUint(decision.remaining, 10),
		)
		c.Header("RateLimit-Reset", strconv.FormatInt(decision.resetAt.Unix(), 10))
		if decision.allowed {
			next(c)
			return
		}
		retryAfter := time.Until(decision.resetAt)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		c.Header(
			"Retry-After",
			strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10),
		)
		httpx.WriteProblem(
			c,
			http.StatusTooManyRequests,
			"RATE_LIMITED",
			"Too many requests",
			"Wait before trying this operation again.",
		)
	}
}
