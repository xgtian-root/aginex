package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xgtian-root/aginex/framework/httpx"
)

const (
	defaultDatabaseTestTimeout   = 10 * time.Second
	defaultInitializationTimeout = 2 * time.Minute
	defaultCleanupTimeout        = 10 * time.Second
	defaultCSRFTokenTTL          = time.Hour
	maximumSetupBodyBytes        = 64 << 10
	maximumSetupHeaderBytes      = 32 << 10
)

type RateLimitConfig struct {
	Limit      uint64
	Window     time.Duration
	MaxEntries int
}

type Config struct {
	Mode        Mode
	Application http.Handler

	AllowedOrigins []string
	TrustedProxies []string
	RequestLimits  httpx.RequestLimits

	CSRFCookieSecure   bool
	CSRFCookieSameSite http.SameSite
	CSRFTokenTTL       time.Duration

	DatabaseTestTimeout   time.Duration
	InitializationTimeout time.Duration
	CleanupTimeout        time.Duration
	RateLimit             RateLimitConfig
	Context               context.Context

	DatabaseTester DatabaseTester
	Initializer    ApplicationInitializer
	Store          InstallationStore
}

type routeState struct {
	mode    Mode
	handler http.Handler
}

type attemptState struct {
	status     Status
	stage      Stage
	code       string
	generation uint64
}

// activeCleanupState is retained after cleanup completes so every concurrent
// or later Shutdown call observes the same result from the single cleanup
// invocation. Closing done publishes err to all waiters.
type activeCleanupState struct {
	done chan struct{}
	err  error
}

// Supervisor owns the one-time setup state machine and is itself an
// http.Handler. Its handler/mode pair is replaced with one atomic store after
// the initialized application's configuration has been durably committed.
type Supervisor struct {
	state atomic.Pointer[routeState]

	modeHandler  http.Handler
	setupHandler http.Handler

	tester      DatabaseTester
	initializer ApplicationInitializer
	store       InstallationStore

	databaseTestTimeout   time.Duration
	initializationTimeout time.Duration
	cleanupTimeout        time.Duration
	now                   func() time.Time

	processContext context.Context
	cancelProcess  context.CancelFunc
	shuttingDown   atomic.Bool
	lifecycleMu    sync.Mutex

	attemptMu sync.RWMutex
	attempt   attemptState
	initWG    sync.WaitGroup

	activeMu sync.Mutex
	active   Candidate
	// activeCleanup is published before the active candidate's cleanup starts
	// and is retained after completion to make the cleanup exactly once and its
	// result stable across Shutdown retries.
	activeCleanup *activeCleanupState

	// sealedCleanupDone is published under lifecycleMu before an unavailable
	// application state becomes visible. It tracks candidates which cannot be
	// placed in active because the installation was sealed without activation or
	// Shutdown had already won the post-commit ownership race.
	sealedCleanupDone chan struct{}

	rateLimiter *memoryRateLimiter
}

func New(config Config) (*Supervisor, error) {
	config = withDefaults(config)
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	processContext, cancelProcess := context.WithCancel(config.Context)
	supervisor := &Supervisor{
		tester:                config.DatabaseTester,
		initializer:           config.Initializer,
		store:                 config.Store,
		databaseTestTimeout:   config.DatabaseTestTimeout,
		initializationTimeout: config.InitializationTimeout,
		cleanupTimeout:        config.CleanupTimeout,
		now:                   time.Now,
		processContext:        processContext,
		cancelProcess:         cancelProcess,
		attempt: attemptState{
			status: StatusRequired,
			stage:  StageWaiting,
		},
		rateLimiter: newMemoryRateLimiter(config.RateLimit, time.Now),
	}

	modeHandler, err := supervisor.newModeHandler(config)
	if err != nil {
		cancelProcess()
		return nil, fmt.Errorf("configure system mode handler: %w", err)
	}
	supervisor.modeHandler = modeHandler

	if config.Mode == ModeSetup {
		setupHandler, setupErr := supervisor.newSetupHandler(config)
		if setupErr != nil {
			cancelProcess()
			return nil, fmt.Errorf("configure setup handler: %w", setupErr)
		}
		supervisor.setupHandler = setupHandler
		supervisor.state.Store(&routeState{
			mode:    ModeSetup,
			handler: setupHandler,
		})
	} else {
		supervisor.state.Store(&routeState{
			mode:    ModeApplication,
			handler: config.Application,
		})
	}

	return supervisor, nil
}

func withDefaults(config Config) Config {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if config.RequestLimits.MaxBodyBytes == 0 ||
		config.RequestLimits.MaxBodyBytes > maximumSetupBodyBytes {
		config.RequestLimits.MaxBodyBytes = maximumSetupBodyBytes
	}
	if config.RequestLimits.MaxHeaderBytes == 0 ||
		config.RequestLimits.MaxHeaderBytes > maximumSetupHeaderBytes {
		config.RequestLimits.MaxHeaderBytes = maximumSetupHeaderBytes
	}
	if config.RequestLimits.MaxHeaderCount == 0 {
		config.RequestLimits.MaxHeaderCount = 100
	}
	if config.CSRFCookieSameSite == http.SameSiteDefaultMode {
		config.CSRFCookieSameSite = http.SameSiteLaxMode
	}
	if config.CSRFTokenTTL == 0 || config.CSRFTokenTTL > defaultCSRFTokenTTL {
		config.CSRFTokenTTL = defaultCSRFTokenTTL
	}
	if config.DatabaseTestTimeout == 0 {
		config.DatabaseTestTimeout = defaultDatabaseTestTimeout
	}
	if config.InitializationTimeout == 0 {
		config.InitializationTimeout = defaultInitializationTimeout
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = defaultCleanupTimeout
	}
	if config.RateLimit.Limit == 0 {
		config.RateLimit.Limit = 10
	}
	if config.RateLimit.Window == 0 {
		config.RateLimit.Window = time.Minute
	}
	if config.RateLimit.MaxEntries == 0 {
		config.RateLimit.MaxEntries = 4096
	}
	return config
}

func validateConfig(config Config) error {
	if config.Mode != ModeSetup && config.Mode != ModeApplication {
		return fmt.Errorf("setup supervisor mode must be %q or %q", ModeSetup, ModeApplication)
	}
	if len(config.AllowedOrigins) == 0 {
		return errors.New("setup supervisor requires at least one allowed origin")
	}
	if config.CSRFTokenTTL <= 0 ||
		config.DatabaseTestTimeout <= 0 ||
		config.InitializationTimeout <= 0 ||
		config.CleanupTimeout <= 0 {
		return errors.New("setup supervisor timeouts must be positive")
	}
	if config.RateLimit.Limit == 0 ||
		config.RateLimit.Window <= 0 ||
		config.RateLimit.MaxEntries < 1 {
		return errors.New("setup supervisor rate limit must be positive")
	}
	if config.Mode == ModeApplication {
		if config.Application == nil {
			return errors.New("application mode requires an HTTP handler")
		}
		return nil
	}
	if config.DatabaseTester == nil {
		return errors.New("setup mode requires a database tester")
	}
	if config.Initializer == nil {
		return errors.New("setup mode requires an application initializer")
	}
	if config.Store == nil {
		return errors.New("setup mode requires an installation store")
	}
	return nil
}

func (s *Supervisor) Handler() http.Handler {
	return s
}

func (s *Supervisor) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == SystemModePath {
		if !isAllowedRouteRequest(request, systemModeRouteAllowed) {
			writeResourceNotFound(writer, request)
			return
		}
		s.modeHandler.ServeHTTP(writer, request)
		return
	}
	state := s.state.Load()
	if state == nil || state.handler == nil {
		writeNetProblem(
			writer,
			request,
			http.StatusServiceUnavailable,
			"SERVICE_UNAVAILABLE",
			"Service unavailable",
			"The service is not ready to handle requests.",
		)
		return
	}
	if state.mode == ModeApplication && isSetupPath(request.URL.Path) {
		writeResourceNotFound(writer, request)
		return
	}
	if state.mode == ModeSetup &&
		!isAllowedRouteRequest(request, setupRouteAllowed) {
		// Route selection deliberately happens before request limits, CORS, and
		// CSRF so no unregistered business path can expose a different response
		// based on application middleware behavior.
		writeResourceNotFound(writer, request)
		return
	}
	state.handler.ServeHTTP(writer, request)
}

type routeAllowedFunc func(method, path string) bool

func isAllowedRouteRequest(
	request *http.Request,
	allowed routeAllowedFunc,
) bool {
	method := request.Method
	if method == http.MethodOptions {
		// Only real CORS preflights for a registered target method reach the
		// secure router. Plain OPTIONS and preflights for an unknown method are
		// indistinguishable from unregistered routes and receive the same 404.
		method = strings.ToUpper(strings.TrimSpace(
			request.Header.Get("Access-Control-Request-Method"),
		))
		if method == "" {
			return false
		}
	}
	return allowed(method, request.URL.Path)
}

func systemModeRouteAllowed(method, path string) bool {
	return method == http.MethodGet && path == SystemModePath
}

func setupRouteAllowed(method, path string) bool {
	switch method {
	case http.MethodGet:
		switch path {
		case "/health/live",
			"/health/ready",
			"/api/v1/health/live",
			"/api/v1/health/ready",
			"/api/v1/auth/csrf",
			SetupStatusPath:
			return true
		}
	case http.MethodPost:
		return path == SetupDatabaseTestPath || path == SetupCompletePath
	}
	return false
}

func isSetupPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/setup")
}

func (s *Supervisor) Mode() Mode {
	state := s.state.Load()
	if state == nil {
		return ""
	}
	return state.mode
}

func (s *Supervisor) Status() SetupStatusResponse {
	s.attemptMu.RLock()
	defer s.attemptMu.RUnlock()
	return SetupStatusResponse{
		Status: s.attempt.status,
		Stage:  s.attempt.stage,
		Code:   s.attempt.code,
	}
}

type beginAttemptResult uint8

const (
	beginAttemptAccepted beginAttemptResult = iota
	beginAttemptInProgress
	beginAttemptNotSetup
)

func (s *Supervisor) beginAttempt() (beginAttemptResult, uint64) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.shuttingDown.Load() || s.Mode() != ModeSetup {
		return beginAttemptNotSetup, 0
	}
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	if s.shuttingDown.Load() || s.Mode() != ModeSetup {
		return beginAttemptNotSetup, 0
	}
	if s.attempt.status == StatusInitializing {
		return beginAttemptInProgress, s.attempt.generation
	}
	s.attempt.generation++
	s.attempt.status = StatusInitializing
	s.attempt.stage = StageValidatingDatabase
	s.attempt.code = ""
	s.initWG.Add(1)
	return beginAttemptAccepted, s.attempt.generation
}

func (s *Supervisor) startInitialization(
	request SetupCompleteRequest,
	metadata RequestMetadata,
	generation uint64,
) {
	go s.runInitialization(request, metadata, generation)
}

func (s *Supervisor) runInitialization(
	request SetupCompleteRequest,
	metadata RequestMetadata,
	generation uint64,
) {
	defer s.initWG.Done()
	defer func() {
		request.Administrator.Password = ""
		request.Database.DSN = ""
		if recover() != nil {
			s.failAttempt(generation, FailureInitialization)
		}
	}()

	ctx, cancel := context.WithTimeout(
		s.processContext,
		s.initializationTimeout,
	)
	defer cancel()
	ctx = contextWithRequestMetadata(ctx, metadata)

	if err := invokeDatabaseTester(s.tester, ctx, request.Database); err != nil {
		s.failAttempt(generation, FailureDatabaseUnavailable)
		return
	}
	if err := ctx.Err(); err != nil {
		s.failAttempt(generation, FailureInitialization)
		return
	}

	s.reportStage(generation, StageMigrating)
	reporter := ProgressReporterFunc(func(stage Stage) {
		s.reportStage(generation, stage)
	})
	candidate, err := invokeApplicationInitializer(
		s.initializer,
		ctx,
		request,
		reporter,
	)
	if err != nil {
		s.shutdownCandidate(candidate)
		s.failAttempt(generation, FailureApplicationInitialization)
		return
	}
	if err := validateCandidate(candidate); err != nil {
		s.shutdownCandidate(candidate)
		s.failAttempt(generation, FailureApplicationInitialization)
		return
	}
	if err := ctx.Err(); err != nil || s.shuttingDown.Load() {
		s.shutdownCandidate(candidate)
		s.failAttempt(generation, FailureInitialization)
		return
	}

	s.reportStage(generation, StagePersistingConfiguration)
	installation := Installation{
		Version:       InstallationVersion,
		Source:        InstallationSourceSetup,
		Database:      request.Database,
		SessionSecret: candidate.SessionSecret,
		InstalledAt:   s.now().UTC(),
	}
	if err := invokeInstallationStore(s.store, ctx, installation); err != nil {
		if errors.Is(err, ErrInstallationSealed) {
			s.sealFailedInstallation(candidate)
			return
		}
		s.shutdownCandidate(candidate)
		s.failAttempt(generation, FailureConfigurationCommit)
		return
	}

	// A successful commit is the point of no return. From here on setup must
	// never become reachable again, even if process shutdown races activation.
	s.reportStage(generation, StageActivatingApplication)
	s.activateCommittedCandidate(candidate)
}

func (s *Supervisor) sealFailedInstallation(candidate Candidate) {
	// The destination exists or publication may already be visible. Retrying
	// Setup could overwrite or diverge from that sealed state, while activating
	// this candidate could serve a different configuration. Close Setup, expose
	// only a generic unavailable application surface, and release the candidate.
	s.lifecycleMu.Lock()
	cleanupDone := make(chan struct{})
	s.sealedCleanupDone = cleanupDone
	s.state.Store(&routeState{
		mode:    ModeApplication,
		handler: unavailableApplicationHandler("The installation is unavailable."),
	})
	s.lifecycleMu.Unlock()
	s.cleanupSealedCandidate(candidate, cleanupDone)
}

func (s *Supervisor) activateCommittedCandidate(candidate Candidate) {
	// Serialize the post-commit ownership handoff with Shutdown. If Shutdown won
	// the race and already returned because its caller deadline elapsed, this
	// goroutine still seals Setup and owns cleanup of the committed candidate.
	s.lifecycleMu.Lock()
	s.activeMu.Lock()
	cleanupCandidate := s.shuttingDown.Load()
	var cleanupDone chan struct{}
	if cleanupCandidate {
		cleanupDone = make(chan struct{})
		s.sealedCleanupDone = cleanupDone
		s.state.Store(&routeState{
			mode:    ModeApplication,
			handler: unavailableApplicationHandler("The service is shutting down."),
		})
	} else {
		s.active = candidate
		s.state.Store(&routeState{
			mode:    ModeApplication,
			handler: candidate.Handler,
		})
	}
	s.activeMu.Unlock()
	s.lifecycleMu.Unlock()

	if cleanupCandidate {
		s.cleanupSealedCandidate(candidate, cleanupDone)
	}
}

func (s *Supervisor) cleanupSealedCandidate(
	candidate Candidate,
	done chan struct{},
) {
	defer close(done)
	s.shutdownCandidate(candidate)
}

func unavailableApplicationHandler(detail string) http.Handler {
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writeNetProblem(
			writer,
			request,
			http.StatusServiceUnavailable,
			"SERVICE_UNAVAILABLE",
			"Service unavailable",
			detail,
		)
	})
}

var errDependencyPanic = errors.New("setup dependency panicked")

func invokeDatabaseTester(
	tester DatabaseTester,
	ctx context.Context,
	database DatabaseConfig,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errDependencyPanic
		}
	}()
	return tester.TestDatabase(ctx, database)
}

func invokeApplicationInitializer(
	initializer ApplicationInitializer,
	ctx context.Context,
	request SetupCompleteRequest,
	reporter ProgressReporter,
) (candidate Candidate, err error) {
	defer func() {
		if recover() != nil {
			candidate = Candidate{}
			err = errDependencyPanic
		}
	}()
	return initializer.InitializeApplication(ctx, request, reporter)
}

func invokeInstallationStore(
	store InstallationStore,
	ctx context.Context,
	installation Installation,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errDependencyPanic
		}
	}()
	return store.CommitInstallation(ctx, installation)
}

func validateCandidate(candidate Candidate) error {
	if candidate.Handler == nil {
		return errors.New("initialized application handler is required")
	}
	if candidate.Shutdown == nil {
		return errors.New("initialized application shutdown callback is required")
	}
	if len(candidate.SessionSecret) < 32 ||
		httpx.CredentialLooksInsecure(candidate.SessionSecret) {
		return errors.New("initialized application session secret is invalid")
	}
	return nil
}

func (s *Supervisor) reportStage(generation uint64, stage Stage) {
	if !validProgressStage(stage) {
		return
	}
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	if s.attempt.status != StatusInitializing ||
		s.attempt.generation != generation {
		return
	}
	if progressStageOrder(stage) < progressStageOrder(s.attempt.stage) {
		return
	}
	s.attempt.stage = stage
}

func progressStageOrder(stage Stage) int {
	switch stage {
	case StageWaiting:
		return 0
	case StageValidatingDatabase:
		return 1
	case StageMigrating:
		return 2
	case StageBootstrapping:
		return 3
	case StageStartingApplication:
		return 4
	case StagePersistingConfiguration:
		return 5
	case StageActivatingApplication:
		return 6
	default:
		return -1
	}
}

func validProgressStage(stage Stage) bool {
	switch stage {
	case StageValidatingDatabase,
		StageMigrating,
		StageBootstrapping,
		StageStartingApplication,
		StagePersistingConfiguration,
		StageActivatingApplication:
		return true
	default:
		return false
	}
}

func (s *Supervisor) failAttempt(generation uint64, code string) {
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	if s.attempt.generation != generation || s.Mode() != ModeSetup {
		return
	}
	s.attempt.status = StatusFailed
	s.attempt.code = code
}

func (s *Supervisor) shutdownCandidate(candidate Candidate) {
	if candidate.Shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout)
	defer cancel()
	_ = invokeCandidateShutdown(candidate, ctx)
}

func invokeCandidateShutdown(
	candidate Candidate,
	ctx context.Context,
) (result error) {
	if candidate.Shutdown == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			result = errors.New("activated application shutdown failed")
		}
	}()
	if err := candidate.Shutdown(ctx); err != nil {
		return errors.New("activated application shutdown failed")
	}
	return nil
}

func (s *Supervisor) activeCandidateCleanup() *activeCleanupState {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()

	if s.activeCleanup != nil {
		return s.activeCleanup
	}
	if s.active.Shutdown == nil {
		return nil
	}

	candidate := s.active
	s.active = Candidate{}
	cleanup := &activeCleanupState{done: make(chan struct{})}
	s.activeCleanup = cleanup
	go s.runActiveCandidateCleanup(candidate, cleanup)
	return cleanup
}

func (s *Supervisor) runActiveCandidateCleanup(
	candidate Candidate,
	cleanup *activeCleanupState,
) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout)
	defer cancel()
	cleanup.err = invokeCandidateShutdown(candidate, ctx)
	close(cleanup.done)
}

func waitForActiveCleanup(
	ctx context.Context,
	cleanup *activeCleanupState,
) error {
	// Prefer an already-completed cleanup even when a retry supplies an expired
	// context. The completed result is the authoritative lifecycle outcome.
	select {
	case <-cleanup.done:
		return cleanup.err
	default:
	}

	select {
	case <-cleanup.done:
		return cleanup.err
	case <-ctx.Done():
		return fmt.Errorf("wait for activated application cleanup: %w", ctx.Err())
	}
}

// Shutdown cancels an in-flight initialization, waits for it to release its
// resources, and shuts down an application activated by this Supervisor.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if s.shuttingDown.CompareAndSwap(false, true) {
		s.cancelProcess()
	}
	mode := s.Mode()
	sealedCleanupDone := s.sealedCleanupDone
	s.lifecycleMu.Unlock()

	if sealedCleanupDone != nil {
		// Check completion before the caller context so a retry made with an
		// already-expired context still observes cleanup which has finished.
		select {
		case <-sealedCleanupDone:
		default:
			select {
			case <-sealedCleanupDone:
			case <-ctx.Done():
				return fmt.Errorf(
					"wait for sealed installation cleanup: %w",
					ctx.Err(),
				)
			}
		}
	}

	// Once application mode is visible, the candidate is fully assigned and
	// only request-secret scrubbing plus WaitGroup bookkeeping remain in the
	// initialization goroutine. Do not let an already-canceled caller context
	// prevent shutdown of that active candidate.
	// mode is sampled under lifecycleMu with sealedCleanupDone so a transition to
	// an unavailable application cannot appear between the two observations.
	if mode != ModeApplication {
		initializationDone := make(chan struct{})
		go func() {
			s.initWG.Wait()
			close(initializationDone)
		}()
		select {
		case <-initializationDone:
		case <-ctx.Done():
			return fmt.Errorf("wait for setup initialization: %w", ctx.Err())
		}
	}

	cleanup := s.activeCandidateCleanup()
	if cleanup == nil {
		return nil
	}
	return waitForActiveCleanup(ctx, cleanup)
}

func writeNetProblem(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	title string,
	detail string,
) {
	requestID := httpx.RequestID(request.Header.Get("X-Request-ID"))
	writer.Header().Set("X-Request-ID", requestID)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", httpx.ProblemMediaType)
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(httpx.Problem{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      code,
		RequestID: requestID,
	})
}

func writeResourceNotFound(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writeNetProblem(
		writer,
		request,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
		"Resource not found",
		"The requested resource does not exist.",
	)
}
