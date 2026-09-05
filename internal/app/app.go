package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	"github.com/xgtian-root/aginex/framework/httpx"
	frameworkidempotency "github.com/xgtian-root/aginex/framework/idempotency"
	"github.com/xgtian-root/aginex/framework/jobs"
	postgresjobs "github.com/xgtian-root/aginex/framework/jobs/postgres"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/framework/observability"
	"github.com/xgtian-root/aginex/framework/ratelimit"
	"github.com/xgtian-root/aginex/framework/services"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/framework/uow"
	"github.com/xgtian-root/aginex/internal/auth"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/internal/platform/database"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"github.com/xgtian-root/aginex/internal/platform/multipartcleanup"
	"github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
)

const principalKey = "aginex.principal"

type App struct {
	cfg             config.Config
	db              *gorm.DB
	sqlDB           *sql.DB
	auth            *auth.Service
	store           storage.Storage
	storageRegistry *storage.Registry
	fileVerifier    *frameworkstorage.FileVerifier
	limiter         ratelimit.Limiter
	idempotency     *frameworkidempotency.GORMStore
	jobs            jobs.TransactionalQueue
	jobInspector    jobs.Inspector
	writes          *uow.UnitOfWork
	registry        *module.Registry
	operations      map[string]module.OperationDefinition
	services        services.Runtime
	observability   *observability.Recorder
	administratorMu sync.Mutex
	activeRequests  atomic.Int64
	lifecycle       appLifecycle
	http            *gin.Engine
	openapi         *huma.OpenAPI
}

func New(cfg config.Config, db *gorm.DB) (*App, error) {
	return NewWithModules(cfg, db)
}

// NewWithModules composes the built-in application with additional compiled-in
// modules. Application modules register their own operations, handlers,
// policies, OpenAPI contracts, resources, jobs, migrations, and lifecycle
// hooks through framework/module without editing Aginex's core router.
func NewWithModules(
	cfg config.Config,
	db *gorm.DB,
	applicationModules ...module.Module,
) (*App, error) {
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		return nil, fmt.Errorf("configure observability: %w", err)
	}
	return NewWithModulesAndObservability(
		cfg,
		db,
		recorder,
		applicationModules...,
	)
}

// NewWithModulesAndObservability composes an application with an explicit
// provider-neutral recorder. Deployment-owned sinks may export the resulting
// spans and metrics without introducing global observability state.
func NewWithModulesAndObservability(
	cfg config.Config,
	db *gorm.DB,
	recorder *observability.Recorder,
	applicationModules ...module.Module,
) (*App, error) {
	return newApplication(
		context.Background(),
		cfg,
		db,
		recorder,
		false,
		applicationModules...,
	)
}

// NewCompositionWithModulesAndObservability composes the fixed Aginex core
// with exactly the supplied application modules. Unlike the legacy internal
// constructors, it never adds the repository starter/example modules.
func NewCompositionWithModulesAndObservability(
	cfg config.Config,
	db *gorm.DB,
	recorder *observability.Recorder,
	applicationModules ...module.Module,
) (*App, error) {
	return newApplication(
		context.Background(),
		cfg,
		db,
		recorder,
		true,
		applicationModules...,
	)
}

// NewCompositionWithModulesAndObservabilityContext composes an application
// while bounding all startup readiness probes by ctx.
func NewCompositionWithModulesAndObservabilityContext(
	ctx context.Context,
	cfg config.Config,
	db *gorm.DB,
	recorder *observability.Recorder,
	applicationModules ...module.Module,
) (*App, error) {
	return newApplication(
		ctx,
		cfg,
		db,
		recorder,
		true,
		applicationModules...,
	)
}

func newApplication(
	ctx context.Context,
	cfg config.Config,
	db *gorm.DB,
	recorder *observability.Recorder,
	exactComposition bool,
	applicationModules ...module.Module,
) (*App, error) {
	if ctx == nil {
		return nil, errors.New("application construction context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if recorder == nil {
		return nil, errors.New("observability recorder is required")
	}
	cfg = config.WithDefaults(cfg)
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate application configuration: %w", err)
	}
	switch cfg.Idempotency.Driver {
	case "disabled", "database":
	default:
		return nil, fmt.Errorf(
			"configure idempotency: unsupported driver %q",
			cfg.Idempotency.Driver,
		)
	}
	var (
		registry   *module.Registry
		operations map[string]module.OperationDefinition
		err        error
	)
	if exactComposition {
		registry, operations, err = composeDefinitionRegistryWithRateLimits(
			cfg.RateLimit,
			applicationModules...,
		)
	} else {
		registry, operations, err = composeRegistryWithRateLimits(
			cfg.RateLimit,
			applicationModules...,
		)
	}
	if err != nil {
		return nil, err
	}
	if cfg.Environment == "production" &&
		registryHasResource(registry, "files") &&
		cfg.Jobs.Driver != "postgres" {
		return nil, fmt.Errorf(
			"validate application composition: files module requires " +
				"AGINEX_JOBS_DRIVER=postgres in production",
		)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := database.InstallObservability(
		db,
		recorder,
		cfg.Database.Driver,
	); err != nil {
		return nil, fmt.Errorf("configure database observability: %w", err)
	}
	if err := migrate.EnsureCurrent(ctx, sqlDB, cfg.Database.Driver); err != nil {
		return nil, fmt.Errorf("check database schema: %w", err)
	}
	if err := ensureModuleMigrationsCurrent(
		ctx,
		sqlDB,
		cfg.Database.Driver,
		registry,
	); err != nil {
		return nil, err
	}
	if err := ratelimit.EnsureCurrent(
		ctx,
		sqlDB,
		cfg.Database.Driver,
	); err != nil {
		return nil, fmt.Errorf("check rate-limit schema: %w", err)
	}
	var idempotencyStore *frameworkidempotency.GORMStore
	if cfg.Idempotency.Driver == "database" {
		if err := frameworkidempotency.EnsureCurrent(
			ctx,
			sqlDB,
			cfg.Database.Driver,
		); err != nil {
			return nil, fmt.Errorf("check idempotency schema: %w", err)
		}
		idempotencyStore, err = frameworkidempotency.NewGORM(
			db,
			frameworkidempotency.WithLeaseDuration(cfg.Idempotency.LeaseDuration),
			frameworkidempotency.WithTTL(cfg.Idempotency.TTL),
		)
		if err != nil {
			return nil, fmt.Errorf("configure idempotency: %w", err)
		}
	}
	var jobQueue jobs.TransactionalQueue
	var jobInspector jobs.Inspector
	if cfg.Jobs.Driver == "postgres" {
		if err := postgresjobs.EnsureCurrent(ctx, sqlDB); err != nil {
			return nil, fmt.Errorf("check postgres jobs schema: %w", err)
		}
		jobStore, storeErr := postgresjobs.New(db, postgresjobs.Config{
			LeaseDuration: cfg.Jobs.LeaseDuration,
		})
		if storeErr != nil {
			return nil, fmt.Errorf("configure postgres jobs: %w", storeErr)
		}
		jobQueue, storeErr = jobs.ObserveTransactionalQueue(
			jobStore,
			recorder,
		)
		if storeErr != nil {
			return nil, fmt.Errorf(
				"configure postgres jobs observability: %w",
				storeErr,
			)
		}
		jobInspector = jobStore
	}
	filePolicy, err := frameworkstorage.NewFilePolicy(
		cfg.FileUploadRuntime().MaxUploadBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("configure file upload policy: %w", err)
	}
	storageRegistry, err := storage.NewRegistry(ctx, cfg, recorder, filePolicy)
	if err != nil {
		return nil, fmt.Errorf("configure storage: %w", err)
	}
	store, err := storageRegistry.Active()
	if err != nil {
		return nil, fmt.Errorf("configure active storage: %w", err)
	}
	fileVerifier, err := frameworkstorage.NewFileVerifier(filePolicy)
	if err != nil {
		return nil, fmt.Errorf("configure file verification: %w", err)
	}
	rateLimitStore, err := ratelimit.NewGORM(db)
	if err != nil {
		return nil, fmt.Errorf("configure rate limiter: %w", err)
	}
	limiter, err := ratelimit.Observe(rateLimitStore, recorder)
	if err != nil {
		return nil, fmt.Errorf("configure rate-limit observability: %w", err)
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return nil, fmt.Errorf("configure unit of work: %w", err)
	}
	if registryHasResource(registry, filesResource) {
		if err := backfillStorageProfileIDs(ctx, db, writes, storageRegistry, cfg.StorageRuntime().LoadedRevision); err != nil {
			return nil, fmt.Errorf("backfill file storage profiles: %w", err)
		}
	}
	runtimeServices, err := services.NewRuntime(
		db,
		writes,
		store,
		jobQueue,
		recorder,
	)
	if err != nil {
		return nil, fmt.Errorf("configure module runtime services: %w", err)
	}

	instance := &App{
		cfg: cfg, db: db, sqlDB: sqlDB,
		auth: auth.New(db, cfg.Session), store: store,
		storageRegistry: storageRegistry, fileVerifier: fileVerifier,
		limiter: limiter, idempotency: idempotencyStore, jobs: jobQueue,
		jobInspector: jobInspector, writes: writes,
		registry: registry, operations: operations,
		services: runtimeServices, observability: recorder,
		lifecycle: appLifecycle{
			cleanupTimeout: cfg.HTTP.ShutdownGracePeriod,
		},
	}
	if registryHasResource(registry, filesResource) {
		cleanupHandler, cleanupErr := multipartcleanup.New(
			db,
			storageRegistry,
			multipartcleanup.Config{
				SystemActorID: "aginex-api-multipart-cleanup",
			},
		)
		if cleanupErr != nil {
			return nil, fmt.Errorf(
				"configure multipart cleanup handler: %w",
				cleanupErr,
			)
		}
		cleanupScanner, cleanupErr := multipartcleanup.NewScanner(
			db,
			cleanupHandler,
			multipartcleanup.ScannerConfig{
				Interval: multipartcleanup.DefaultScanInterval,
				Batch:    multipartcleanup.DefaultScanBatch,
			},
		)
		if cleanupErr != nil {
			return nil, fmt.Errorf(
				"configure multipart cleanup scanner: %w",
				cleanupErr,
			)
		}
		if cleanupErr := registry.RegisterLifecycleHook(module.LifecycleHook{
			Name:  "storage.multipart.cleanup",
			Start: cleanupScanner.Start,
			Stop:  cleanupScanner.Stop,
		}); cleanupErr != nil {
			return nil, fmt.Errorf(
				"register multipart cleanup scanner: %w",
				cleanupErr,
			)
		}
	}
	instance.http, err = instance.routes()
	if err != nil {
		return nil, fmt.Errorf("configure HTTP router: %w", err)
	}
	return instance, nil
}

func registryHasResource(
	registry *module.Registry,
	name string,
) bool {
	if registry == nil {
		return false
	}
	for _, resource := range registry.Resources() {
		if resource.Name == name {
			return true
		}
	}
	return false
}

func (a *App) Handler() http.Handler {
	return a.http
}

func (a *App) OpenAPI() *huma.OpenAPI {
	return a.openapi
}

func (a *App) routes() (*gin.Engine, error) {
	if a.cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else if a.cfg.Environment == "test" || a.cfg.Environment == "contract" {
		gin.SetMode(gin.TestMode)
	}
	router := gin.New()
	if err := router.SetTrustedProxies(a.cfg.HTTP.TrustedProxies); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	requestLimits, err := httpx.NewRequestLimits(httpx.RequestLimits{
		MaxBodyBytes:   a.cfg.HTTP.MaxBodyBytes,
		MaxHeaderBytes: a.cfg.HTTP.MaxHeaderBytes,
		MaxHeaderCount: a.cfg.HTTP.MaxHeaderCount,
	})
	if err != nil {
		return nil, err
	}
	binaryUploadLimits, err := httpx.NewRequestLimits(httpx.RequestLimits{
		MaxBodyBytes:   absoluteMaxUploadBytes,
		MaxHeaderBytes: a.cfg.HTTP.MaxHeaderBytes,
		MaxHeaderCount: a.cfg.HTTP.MaxHeaderCount,
	})
	if err != nil {
		return nil, err
	}
	cors, err := httpx.NewCORS(httpx.CORSConfig{
		AllowedOrigins:   a.cfg.AllowedWebOrigins(),
		AllowCredentials: true,
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Idempotency-Key",
			"If-Match",
			httpx.TraceParentHeader,
			httpx.CSRFHeaderName,
			"X-Request-ID",
		},
		ExposedHeaders: []string{
			"ETag",
			"Idempotency-Replayed",
			"Retry-After",
			"X-Request-ID",
			httpx.TraceParentHeader,
		},
	})
	if err != nil {
		return nil, err
	}
	csrf, err := httpx.NewCSRF(httpx.CSRFConfig{
		TokenCookieName:      httpx.CSRFCookieName,
		HeaderName:           httpx.CSRFHeaderName,
		SessionCookieNames:   []string{httpx.SessionCookieName},
		AllowedOrigins:       a.cfg.AllowedWebOrigins(),
		AllowBearer:          true,
		AllowUnauthenticated: true,
	})
	if err != nil {
		return nil, err
	}
	router.Use(
		a.requestContext(),
		// Gin's default recovery dumps the complete request on some panic
		// paths, including signed query capabilities and cookies. Recovery is
		// intentionally silent and returns only the stable public problem.
		gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			httpx.AbortProblem(
				c,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Request failed",
				"The request could not be processed.",
			)
		}),
		routeAwareRequestLimits(requestLimits, binaryUploadLimits),
		cors,
		csrf,
	)
	router.GET("/health/live", a.live)
	router.GET("/health/ready", a.ready)

	api := humagin.New(router, huma.DefaultConfig("Aginex API", "1.0.0"))
	a.openapi = api.OpenAPI()
	if err := a.bindBuiltInHTTPRoutes(); err != nil {
		return nil, fmt.Errorf("bind built-in HTTP routes: %w", err)
	}
	if err := a.registry.ValidateRuntime(); err != nil {
		return nil, fmt.Errorf("validate module runtime: %w", err)
	}
	documentGinOperations(api.OpenAPI(), a.registry)

	if err := a.mountRegisteredOperations(router); err != nil {
		return nil, fmt.Errorf("mount registered HTTP operations: %w", err)
	}
	return router, nil
}

func routeAwareRequestLimits(
	standard gin.HandlerFunc,
	binaryUpload gin.HandlerFunc,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPut &&
			(strings.HasPrefix(c.Request.URL.Path, "/api/v1/files/local-upload/") ||
				strings.HasPrefix(c.Request.URL.Path, "/api/v1/files/upload-sessions/")) {
			binaryUpload(c)
			return
		}
		standard(c)
	}
}

func (a *App) requestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := httpx.RequestID(c.GetHeader("X-Request-ID"))
		c.Header("X-Request-ID", requestID)

		recorder := a.observability
		if recorder == nil {
			recorder, _ = observability.NewRecorder(nil)
		}
		method := observableHTTPMethod(c.Request.Method)
		startAttributes, _ := observability.NewAttributes(map[string]string{
			"http.method": method,
		})
		traceContext := recorder.Extract(
			c.Request.Context(),
			c.GetHeader(httpx.TraceParentHeader),
		)
		traceContext, span := recorder.Start(
			traceContext,
			observability.SpanStart{
				Name:       method + " HTTP",
				Kind:       observability.SpanKindServer,
				Attributes: startAttributes,
			},
		)
		traceContext = a.contextWithRuntimeServices(traceContext)
		c.Request = c.Request.WithContext(traceContext)
		traceParent := observability.TraceParentFromContext(traceContext)
		c.Request.Header.Set(httpx.TraceParentHeader, traceParent)
		c.Header(httpx.TraceParentHeader, traceParent)

		started := time.Now()
		active := a.activeRequests.Add(1)
		a.recordMetric(observability.Metric{
			Name:       "http.server.active_requests",
			Kind:       observability.MetricGauge,
			Value:      float64(active),
			Unit:       "request",
			Attributes: startAttributes,
		})
		c.Next()
		duration := time.Since(started)
		active = a.activeRequests.Add(-1)

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		finalAttributes, _ := observability.NewAttributes(map[string]string{
			"http.method": method,
			"http.route":  route,
			"http.status": strconv.Itoa(status),
			"outcome":     observableHTTPOutcome(status),
		})
		span.SetName(method + " " + route)
		_ = span.SetAttributes(finalAttributes)
		spanEnd := observability.SpanEnd{
			Outcome: observability.OutcomeOK,
		}
		if status >= http.StatusInternalServerError {
			spanEnd.Outcome = observability.OutcomeError
			spanEnd.Err = errors.New("HTTP server error")
		}
		span.End(spanEnd)
		a.recordMetric(observability.Metric{
			Name:       "http.server.requests",
			Kind:       observability.MetricCounter,
			Value:      1,
			Unit:       "request",
			Attributes: finalAttributes,
		})
		a.recordMetric(observability.Metric{
			Name:       "http.server.duration",
			Kind:       observability.MetricHistogram,
			Value:      duration.Seconds(),
			Unit:       "s",
			Attributes: finalAttributes,
		})
		a.recordMetric(observability.Metric{
			Name:       "http.server.active_requests",
			Kind:       observability.MetricGauge,
			Value:      float64(active),
			Unit:       "request",
			Attributes: startAttributes,
		})

		principal := currentPrincipal(c)
		actorID := ""
		if actor, ok := module.AuthenticatedActorFromContext(
			c.Request.Context(),
		); ok {
			actorID = actor.ID
		} else if principal.User.ID != "" {
			actorID = principal.User.ID
		}
		slog.Info("HTTP request",
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"duration", duration,
			"request_id", requestID,
			"traceparent", traceParent,
			"actor_id", actorID,
		)
	}
}

func (a *App) contextWithRuntimeServices(ctx context.Context) context.Context {
	if a == nil || a.services.Database == nil || a.services.Storage == nil {
		return ctx
	}
	result, err := services.ContextWithRuntime(ctx, a.services)
	if err != nil {
		return ctx
	}
	return result
}

func (a *App) recordMetric(metric observability.Metric) {
	if a == nil || a.observability == nil {
		return
	}
	if err := a.observability.RecordMetric(metric); err != nil {
		slog.Error(
			"Observability metric rejected",
			"metric", metric.Name,
			"error", httpx.RedactedValue,
		)
	}
}

func observableHTTPMethod(method string) string {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet,
		http.MethodHead, http.MethodOptions, http.MethodPatch,
		http.MethodPost, http.MethodPut, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func observableHTTPOutcome(status int) string {
	switch {
	case status >= http.StatusInternalServerError:
		return "server_error"
	case status >= http.StatusBadRequest:
		return "client_error"
	default:
		return "success"
	}
}

func (a *App) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(a.cfg.Session.CookieName)
		if err != nil {
			writeProblem(c, http.StatusUnauthorized, "Authentication required", "Sign in to continue.")
			c.Abort()
			return
		}
		principal, err := a.auth.AuthenticateContext(
			c.Request.Context(),
			token,
		)
		if err != nil {
			writeProblem(c, http.StatusUnauthorized, "Session expired", "Sign in again to continue.")
			c.Abort()
			return
		}
		c.Set(principalKey, principal)
		requestContext, err := module.ContextWithAuthenticatedActor(
			c.Request.Context(),
			principal.Actor(),
		)
		if err != nil {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authentication unavailable",
				"The authenticated actor is invalid.",
			)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	}
}

func (a *App) login(c *gin.Context) {
	input, ok := validatedRequestDTO[LoginRequest](c)
	if !ok {
		return
	}
	if !a.enforceLoginAccountRateLimit(c, input.Email) {
		return
	}
	var result auth.LoginResult
	var response UserResponse
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var loginErr error
		result, loginErr = a.auth.LoginTx(
			tx,
			input.Email,
			input.Password,
			c.ClientIP(),
			c.Request.UserAgent(),
		)
		if loginErr != nil {
			return frameworkaudit.Event{}, loginErr
		}
		response, loginErr = a.userResponseTx(tx, result.User.ID)
		if loginErr != nil {
			return frameworkaudit.Event{}, loginErr
		}
		return successfulAuditEvent(
			c,
			&result.User.ID,
			"auth:login",
			"session",
			result.SessionID,
			"User signed in",
			nil,
			map[string]any{"userId": result.User.ID},
		), nil
	})
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeProblem(c, http.StatusUnauthorized, "Sign-in failed", "The email or password is incorrect.")
		return
	}
	if err != nil {
		logRequestFailure(c, "login", err)
		writeProblem(c, http.StatusInternalServerError, "Sign-in failed", "The session could not be created.")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     a.cfg.Session.CookieName,
		Value:    result.Token,
		Path:     "/",
		MaxAge:   int(a.cfg.Session.TTL.Seconds()),
		Expires:  time.Now().UTC().Add(a.cfg.Session.TTL),
		HttpOnly: true,
		Secure:   a.cfg.Session.Secure,
		SameSite: a.cfg.Session.SameSite,
	})
	c.JSON(http.StatusOK, response)
}

func (a *App) logout(c *gin.Context) {
	token, _ := c.Cookie(a.cfg.Session.CookieName)
	principal := currentPrincipal(c)
	if err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := a.auth.LogoutTx(tx, token); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"auth:logout",
			"session",
			principal.SessionID,
			"User signed out",
			map[string]any{"userId": principal.User.ID},
			nil,
		), nil
	}); err != nil {
		logRequestFailure(c, "logout", err)
		writeProblem(c, http.StatusInternalServerError, "Sign-out failed", "The session could not be revoked.")
		return
	}
	a.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func (a *App) revokeAllSessions(c *gin.Context) {
	principal := currentPrincipal(c)
	if err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		revoked, err := a.auth.RevokeAllSessionsTx(tx, principal.User.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"sessions:revoke-all",
			"session",
			principal.User.ID,
			"User revoked all browser sessions",
			map[string]any{
				"userId":       principal.User.ID,
				"sessionCount": revoked,
			},
			nil,
		), nil
	}); err != nil {
		logRequestFailure(c, "revoke_all_sessions", err)
		writeProblem(c, http.StatusInternalServerError, "Sign-out failed", "The sessions could not be revoked.")
		return
	}
	a.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func (a *App) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: a.cfg.Session.CookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0).UTC(), HttpOnly: true, Secure: a.cfg.Session.Secure,
		SameSite: a.cfg.Session.SameSite,
	})
}

func (a *App) me(c *gin.Context) {
	principal := currentPrincipal(c)
	response, err := a.userResponseTx(
		a.db.WithContext(c.Request.Context()),
		principal.User.ID,
	)
	if err != nil {
		if accessNotFound(err) {
			writeProblem(
				c,
				http.StatusUnauthorized,
				"Session expired",
				"Sign in again to continue.",
			)
			return
		}
		writeAccessFailure(c, "get_current_user", err)
		return
	}
	permissions := make([]string, 0, len(principal.Permissions))
	for code := range principal.Permissions {
		permissions = append(permissions, code)
	}
	sort.Strings(permissions)
	response.Permissions = permissions
	response.Grants = make([]UserGrantResponse, 0, len(principal.GrantScopes))
	for _, code := range permissions {
		if scope, ok := principal.GrantScopes[code]; ok {
			response.Grants = append(response.Grants, UserGrantResponse{
				Permission: code,
				Scope:      string(scope),
			})
		}
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) listProducts(c *gin.Context) {
	page, pageSize := pagination(c)
	query := a.db.WithContext(c.Request.Context()).Model(&domain.Product{})
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR sku LIKE ?", like, like)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		logRequestFailure(c, "list_products_count", err)
		writeProblem(c, http.StatusInternalServerError, "Products unavailable", "The product list could not be loaded.")
		return
	}
	var products []domain.Product
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&products).Error; err != nil {
		logRequestFailure(c, "list_products_query", err)
		writeProblem(c, http.StatusInternalServerError, "Products unavailable", "The product list could not be loaded.")
		return
	}
	items := make([]ProductResponse, 0, len(products))
	for _, product := range products {
		items = append(items, productResponse(product))
	}
	c.JSON(http.StatusOK, Page[ProductResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) createProduct(c *gin.Context) {
	input, ok := validatedRequestDTO[ProductRequest](c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	product := domain.Product{
		ID: uuid.NewString(), Name: strings.TrimSpace(input.Name), SKU: strings.TrimSpace(input.SKU),
		PriceCents: input.PriceCents, Status: input.Status, CreatedAt: now, UpdatedAt: now,
	}
	principal := currentPrincipal(c)
	if err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.Create(&product).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusCreated,
			productResponse(product),
			http.Header{"Location": {"/api/v1/products/" + product.ID}},
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"products:create",
			"product",
			product.ID,
			"Created "+product.SKU,
			nil,
			productAuditFields(product),
		), nil
	}); err != nil {
		logRequestFailure(c, "create_product", err)
		writeProblem(c, http.StatusInternalServerError, "Product could not be created", "The product write could not be committed.")
		return
	}
	c.Header("Location", "/api/v1/products/"+product.ID)
	c.JSON(http.StatusCreated, productResponse(product))
}

func (a *App) getProduct(c *gin.Context) {
	var product domain.Product
	if err := a.db.WithContext(c.Request.Context()).
		First(&product, "id = ?", c.Param("id")).
		Error; err != nil {
		notFoundOrInternal(c, "Product", err)
		return
	}
	c.JSON(http.StatusOK, productResponse(product))
}

func (a *App) updateProduct(c *gin.Context) {
	input, ok := validatedRequestDTO[ProductRequest](c)
	if !ok {
		return
	}
	var product domain.Product
	principal := currentPrincipal(c)
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.First(&product, "id = ?", c.Param("id")).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		before := productAuditFields(product)
		product.Name = strings.TrimSpace(input.Name)
		product.SKU = strings.TrimSpace(input.SKU)
		product.PriceCents = input.PriceCents
		product.Status = input.Status
		product.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&product).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusOK,
			productResponse(product),
			nil,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"products:update",
			"product",
			product.ID,
			"Updated "+product.SKU,
			before,
			productAuditFields(product),
		), nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			notFoundOrInternal(c, "Product", err)
			return
		}
		logRequestFailure(c, "update_product", err)
		writeProblem(c, http.StatusInternalServerError, "Product could not be updated", "The product write could not be committed.")
		return
	}
	c.JSON(http.StatusOK, productResponse(product))
}

func (a *App) deleteProduct(c *gin.Context) {
	var product domain.Product
	principal := currentPrincipal(c)
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.First(&product, "id = ?", c.Param("id")).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := tx.Delete(&product).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusNoContent,
			nil,
			nil,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"products:delete",
			"product",
			product.ID,
			"Deleted "+product.SKU,
			productAuditFields(product),
			nil,
		), nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			notFoundOrInternal(c, "Product", err)
			return
		}
		logRequestFailure(c, "delete_product", err)
		writeProblem(c, http.StatusInternalServerError, "Product could not be deleted", "The product write could not be committed.")
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) listAuditLogs(c *gin.Context) {
	page, pageSize := pagination(c)
	var total int64
	requestDB := a.db.WithContext(c.Request.Context())
	if err := requestDB.Model(&domain.AuditLog{}).Count(&total).Error; err != nil {
		logRequestFailure(c, "list_audit_logs_count", err)
		writeProblem(c, http.StatusInternalServerError, "Audit history unavailable", "The audit history could not be loaded.")
		return
	}
	var logs []domain.AuditLog
	if err := requestDB.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs).
		Error; err != nil {
		logRequestFailure(c, "list_audit_logs_query", err)
		writeProblem(c, http.StatusInternalServerError, "Audit history unavailable", "The audit history could not be loaded.")
		return
	}
	items := make([]AuditLogResponse, 0, len(logs))
	for _, log := range logs {
		items = append(items, auditLogResponse(log))
	}
	c.JSON(http.StatusOK, Page[AuditLogResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) dashboardSummary(c *gin.Context) {
	var productCount, userCount, eventCount int64
	requestDB := a.db.WithContext(c.Request.Context())
	if err := requestDB.Model(&domain.Product{}).
		Count(&productCount).
		Error; err != nil {
		logRequestFailure(c, "dashboard_product_count", err)
		writeProblem(c, http.StatusInternalServerError, "Dashboard unavailable", "The dashboard summary could not be loaded.")
		return
	}
	if err := requestDB.Model(&domain.User{}).
		Count(&userCount).
		Error; err != nil {
		logRequestFailure(c, "dashboard_user_count", err)
		writeProblem(c, http.StatusInternalServerError, "Dashboard unavailable", "The dashboard summary could not be loaded.")
		return
	}
	if err := requestDB.Model(&domain.AuditLog{}).
		Where("created_at >= ?", time.Now().UTC().Add(-24*time.Hour)).
		Count(&eventCount).Error; err != nil {
		logRequestFailure(c, "dashboard_audit_count", err)
		writeProblem(c, http.StatusInternalServerError, "Dashboard unavailable", "The dashboard summary could not be loaded.")
		return
	}
	c.JSON(http.StatusOK, DashboardSummaryResponse{
		Products:          productCount,
		Users:             userCount,
		EventsLast24Hours: eventCount,
		GeneratedAt:       time.Now().UTC(),
	})
}

func successfulAuditEvent(
	c *gin.Context,
	actorID *string,
	action string,
	resource string,
	resourceID string,
	summary string,
	before map[string]any,
	after map[string]any,
) frameworkaudit.Event {
	return frameworkaudit.Event{
		ActorID:    actorID,
		ActorKind:  frameworkaudit.ActorUser,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Result:     frameworkaudit.ResultSuccess,
		RequestID:  c.Writer.Header().Get("X-Request-ID"),
		Source:     "http",
		Summary:    summary,
		IPAddress:  c.ClientIP(),
		Before:     before,
		After:      after,
	}
}

func productAuditFields(product domain.Product) map[string]any {
	return map[string]any{
		"id":         product.ID,
		"name":       product.Name,
		"sku":        product.SKU,
		"priceCents": product.PriceCents,
		"status":     product.Status,
	}
}

func productResponse(product domain.Product) ProductResponse {
	return ProductResponse{
		ID:         product.ID,
		Name:       product.Name,
		SKU:        product.SKU,
		PriceCents: product.PriceCents,
		Status:     product.Status,
		CreatedAt:  product.CreatedAt,
		UpdatedAt:  product.UpdatedAt,
	}
}

func auditLogResponse(log domain.AuditLog) AuditLogResponse {
	return AuditLogResponse{
		ID:         log.ID,
		ActorID:    log.ActorID,
		ActorKind:  log.ActorKind,
		Action:     log.Action,
		Resource:   log.Resource,
		ResourceID: log.ResourceID,
		Result:     log.Result,
		RequestID:  log.RequestID,
		Source:     log.Source,
		Summary:    log.Summary,
		IPAddress:  log.IPAddress,
		Before:     map[string]any(log.Before),
		After:      map[string]any(log.After),
		CreatedAt:  log.CreatedAt,
	}
}

func currentPrincipal(c *gin.Context) auth.Principal {
	value, _ := c.Get(principalKey)
	principal, _ := value.(auth.Principal)
	return principal
}

func pagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func notFoundOrInternal(c *gin.Context, resource string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeProblem(c, http.StatusNotFound, resource+" not found", fmt.Sprintf("No %s matches this identifier.", strings.ToLower(resource)))
		return
	}
	logRequestFailure(c, "load_"+strings.ToLower(resource), err)
	writeProblem(c, http.StatusInternalServerError, resource+" unavailable", "The requested resource could not be loaded.")
}

func writeProblem(c *gin.Context, status int, title, detail string) {
	if status >= http.StatusInternalServerError {
		detail = "The request could not be completed because of an internal error."
	}
	httpx.WriteProblem(c, status, "", title, detail)
}

func logRequestFailure(
	c *gin.Context,
	operation string,
	err error,
) {
	if err == nil {
		return
	}
	requestID := ""
	ctx := context.Background()
	if c != nil {
		requestID = c.Writer.Header().Get("X-Request-ID")
		if c.Request != nil {
			ctx = c.Request.Context()
		}
	}
	slog.ErrorContext(
		ctx,
		"HTTP operation failed",
		"operation",
		operation,
		"request_id",
		requestID,
		"error_type",
		fmt.Sprintf("%T", err),
		"error",
		httpx.RedactedValue,
	)
}

func writeBindingProblem(c *gin.Context, title string, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		httpx.WriteProblem(
			c,
			http.StatusRequestEntityTooLarge,
			"REQUEST_TOO_LARGE",
			"Request body is too large",
			"Reduce the request body size and try again.",
		)
		return
	}
	logRequestFailure(c, "decode_request_body", err)
	httpx.WriteProblem(
		c,
		http.StatusBadRequest,
		"REQUEST_INVALID",
		title,
		"The request body is invalid.",
	)
}
