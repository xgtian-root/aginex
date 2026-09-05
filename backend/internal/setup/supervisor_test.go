package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
)

const (
	testOrigin        = "https://admin.example"
	testSessionSecret = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	testDSN           = "postgres://aginex:database-secret@db.example:5432/aginex?sslmode=require"
	testPassword      = "correct horse battery staple"
)

func TestSetupSurfaceAndStableModeProbe(t *testing.T) {
	supervisor := newTestSupervisor(t, Config{Mode: ModeSetup})

	mode := performRequest(supervisor, http.MethodGet, SystemModePath, nil)
	assertStatus(t, mode, http.StatusOK)
	assertNoStore(t, mode)
	var modeResponse SystemModeResponse
	decodeResponse(t, mode, &modeResponse)
	if modeResponse.Mode != ModeSetup {
		t.Fatalf("mode = %q, want %q", modeResponse.Mode, ModeSetup)
	}

	status := performRequest(supervisor, http.MethodGet, SetupStatusPath, nil)
	assertStatus(t, status, http.StatusOK)
	assertNoStore(t, status)
	var statusResponse SetupStatusResponse
	decodeResponse(t, status, &statusResponse)
	if statusResponse.Status != StatusRequired || statusResponse.Stage != StageWaiting {
		t.Fatalf("status = %#v", statusResponse)
	}

	for _, path := range []string{
		"/health/live",
		"/health/ready",
		"/api/v1/health/live",
		"/api/v1/health/ready",
	} {
		response := performRequest(supervisor, http.MethodGet, path, nil)
		assertStatus(t, response, http.StatusOK)
		assertNoStore(t, response)
	}

	for _, path := range []string{
		"/api/v1/dashboard/summary",
		"/api/v1/auth/me",
		"/openapi.json",
	} {
		response := performRequest(supervisor, http.MethodGet, path, nil)
		assertProblem(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		assertNoStore(t, response)
	}

	disallowed := performRequestWithHeaders(
		supervisor,
		http.MethodGet,
		SystemModePath,
		nil,
		map[string]string{"Origin": "https://evil.example"},
		nil,
	)
	assertProblem(t, disallowed, http.StatusForbidden, "CORS_FORBIDDEN")
}

func TestSetupOuterAllowlistRejectsUnknownRoutesBeforeSecurityMiddleware(
	t *testing.T,
) {
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   32,
			MaxHeaderBytes: 1024,
			MaxHeaderCount: 16,
		},
	})

	unknownWrite := performRequestWithHeaders(
		supervisor,
		http.MethodPost,
		"/api/v1/products",
		bytes.NewReader(bytes.Repeat([]byte("x"), 128)),
		map[string]string{
			"Content-Type": "application/json",
			"Origin":       "https://evil.example",
		},
		nil,
	)
	assertProblem(
		t,
		unknownWrite,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
	)
	assertNoStore(t, unknownWrite)

	wrongMethod := performRequestWithHeaders(
		supervisor,
		http.MethodDelete,
		SetupStatusPath,
		nil,
		map[string]string{"Origin": testOrigin},
		nil,
	)
	assertProblem(
		t,
		wrongMethod,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
	)
	assertNoStore(t, wrongMethod)

	wrongModeMethod := performRequest(
		supervisor,
		http.MethodPost,
		SystemModePath,
		nil,
	)
	assertProblem(
		t,
		wrongModeMethod,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
	)
	assertNoStore(t, wrongModeMethod)

	unknownPreflight := performRequestWithHeaders(
		supervisor,
		http.MethodOptions,
		"/api/v1/products",
		nil,
		map[string]string{
			"Origin":                        testOrigin,
			"Access-Control-Request-Method": http.MethodPost,
		},
		nil,
	)
	assertProblem(
		t,
		unknownPreflight,
		http.StatusNotFound,
		"RESOURCE_NOT_FOUND",
	)
	assertNoStore(t, unknownPreflight)

	registeredPreflight := performRequestWithHeaders(
		supervisor,
		http.MethodOptions,
		SetupDatabaseTestPath,
		nil,
		map[string]string{
			"Origin":                         testOrigin,
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "Content-Type, " + httpx.CSRFHeaderName,
		},
		nil,
	)
	assertStatus(t, registeredPreflight, http.StatusNoContent)
	assertNoStore(t, registeredPreflight)
}

func TestDatabaseTestRequiresCSRFValidJSONAndReturnsSafeErrors(t *testing.T) {
	var calls atomic.Int32
	config := Config{
		Mode: ModeSetup,
		DatabaseTester: DatabaseTesterFunc(func(
			_ context.Context,
			database DatabaseConfig,
		) error {
			calls.Add(1)
			if database.Driver != "postgres" || database.DSN != testDSN {
				t.Fatalf("database = %#v", database)
			}
			return errors.New("driver leaked secret: " + testDSN)
		}),
	}
	supervisor := newTestSupervisor(t, config)
	body := marshalJSON(t, SetupDatabaseTestRequest{
		Database: testPostgresInput(),
	})

	missingCSRF := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		nil,
		"",
	)
	assertProblem(t, missingCSRF, http.StatusForbidden, "CSRF_FORBIDDEN")
	if calls.Load() != 0 {
		t.Fatal("database tester ran without CSRF")
	}

	cookie, token := getCSRF(t, supervisor)
	failed := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		cookie,
		token,
	)
	assertProblem(
		t,
		failed,
		http.StatusUnprocessableEntity,
		"SETUP_DATABASE_UNAVAILABLE",
	)
	if strings.Contains(failed.Body.String(), testDSN) ||
		strings.Contains(failed.Body.String(), "driver leaked") {
		t.Fatalf("database error leaked into response: %s", failed.Body.String())
	}

	wrongOrigin := performRequestWithHeaders(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		bytes.NewReader(body),
		map[string]string{
			"Content-Type":       "application/json",
			"Origin":             "https://evil.example",
			httpx.CSRFHeaderName: token,
		},
		cookie,
	)
	assertProblem(t, wrongOrigin, http.StatusForbidden, "CORS_FORBIDDEN")

	legacyDSN := []byte(`{"database":{"driver":"postgres","dsn":"safe"}}`)
	invalid := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		legacyDSN,
		cookie,
		token,
	)
	assertProblem(t, invalid, http.StatusBadRequest, "REQUEST_INVALID")
	if calls.Load() != 1 {
		t.Fatal("legacy DSN request reached the database tester")
	}

	unsupported := performRequestWithHeaders(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		bytes.NewReader(body),
		map[string]string{
			"Content-Type":       "text/plain",
			"Origin":             testOrigin,
			httpx.CSRFHeaderName: token,
		},
		cookie,
	)
	assertProblem(
		t,
		unsupported,
		http.StatusUnsupportedMediaType,
		"UNSUPPORTED_MEDIA_TYPE",
	)
}

func TestSetupWritesRequireExplicitOriginEvenWithValidReferer(t *testing.T) {
	var calls atomic.Int32
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		DatabaseTester: DatabaseTesterFunc(func(
			_ context.Context,
			_ DatabaseConfig,
		) error {
			calls.Add(1)
			return nil
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	body := marshalJSON(t, SetupDatabaseTestRequest{
		Database: testPostgresInput(),
	})
	response := performRequestWithHeaders(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		bytes.NewReader(body),
		map[string]string{
			"Content-Type":       "application/json",
			"Referer":            testOrigin + "/setup",
			httpx.CSRFHeaderName: token,
		},
		cookie,
	)
	assertProblem(t, response, http.StatusForbidden, "CSRF_FORBIDDEN")
	if calls.Load() != 0 {
		t.Fatal("database tester ran without an explicit Origin header")
	}
}

func TestDatabaseTestSuccessAndRequestLimits(t *testing.T) {
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		DatabaseTester: DatabaseTesterFunc(func(
			_ context.Context,
			_ DatabaseConfig,
		) error {
			return nil
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	body := marshalJSON(t, SetupDatabaseTestRequest{
		Database: DatabaseInput{
			Driver: "sqlite",
			SQLite: &SQLiteDatabaseInput{
				Directory: "data",
				Filename:  "aginex.db",
			},
		},
	})
	response := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		cookie,
		token,
	)
	assertStatus(t, response, http.StatusOK)
	var result SetupDatabaseTestResponse
	decodeResponse(t, response, &result)
	if result.Status != "ok" {
		t.Fatalf("result = %#v", result)
	}

	limited := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   32,
			MaxHeaderBytes: 4096,
			MaxHeaderCount: 32,
		},
	})
	limitCookie, limitToken := getCSRF(t, limited)
	oversized := performJSONRequest(
		limited,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		limitCookie,
		limitToken,
	)
	assertProblem(t, oversized, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE")
}

func TestSetupSecurityLimitsClampBroadApplicationValuesAndPreserveStricterOnes(
	t *testing.T,
) {
	broad := withDefaults(Config{
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   12 << 20,
			MaxHeaderBytes: 1 << 20,
			MaxHeaderCount: 77,
		},
		CSRFTokenTTL: 24 * time.Hour,
	})
	if broad.RequestLimits.MaxBodyBytes != maximumSetupBodyBytes ||
		broad.RequestLimits.MaxHeaderBytes != maximumSetupHeaderBytes ||
		broad.RequestLimits.MaxHeaderCount != 77 ||
		broad.CSRFTokenTTL != defaultCSRFTokenTTL {
		t.Fatalf("broad setup limits were not clamped: %#v", broad)
	}

	stricter := withDefaults(Config{
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   8 << 10,
			MaxHeaderBytes: 4 << 10,
			MaxHeaderCount: 20,
		},
		CSRFTokenTTL: 15 * time.Minute,
	})
	if stricter.RequestLimits.MaxBodyBytes != 8<<10 ||
		stricter.RequestLimits.MaxHeaderBytes != 4<<10 ||
		stricter.RequestLimits.MaxHeaderCount != 20 ||
		stricter.CSRFTokenTTL != 15*time.Minute {
		t.Fatalf("stricter setup limits were overwritten: %#v", stricter)
	}
}

func TestCompleteRunsDetachedPersistsThenAtomicallyActivates(t *testing.T) {
	var supervisor *Supervisor
	initializerStarted := make(chan struct{})
	storeStarted := make(chan Installation, 1)
	releaseStore := make(chan struct{})
	shutdownCalled := make(chan struct{}, 1)
	metadataReceived := make(chan RequestMetadata, 1)

	config := Config{
		Mode: ModeSetup,
		DatabaseTester: DatabaseTesterFunc(func(
			ctx context.Context,
			database DatabaseConfig,
		) error {
			if ctx.Err() != nil {
				t.Errorf("detached database test context already canceled: %v", ctx.Err())
			}
			if database.DSN != testDSN {
				t.Errorf("database DSN = %q", database.DSN)
			}
			return nil
		}),
		Initializer: ApplicationInitializerFunc(func(
			ctx context.Context,
			request SetupCompleteRequest,
			reporter ProgressReporter,
		) (Candidate, error) {
			close(initializerStarted)
			metadata, ok := RequestMetadataFromContext(ctx)
			if !ok {
				t.Error("trusted request metadata missing")
			}
			metadataReceived <- metadata
			if request.Administrator.Password != testPassword {
				t.Error("initializer did not receive administrator password")
			}
			reporter.Report(StageBootstrapping)
			reporter.Report(StageStartingApplication)
			return Candidate{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"application":true}`))
				}),
				SessionSecret: testSessionSecret,
				Shutdown: func(context.Context) error {
					shutdownCalled <- struct{}{}
					return nil
				},
			}, nil
		}),
		Store: InstallationStoreFunc(func(
			ctx context.Context,
			installation Installation,
		) error {
			if supervisor.Mode() != ModeSetup {
				t.Error("application activated before installation commit")
			}
			storeStarted <- installation
			select {
			case <-releaseStore:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
	}
	supervisor = newTestSupervisor(t, config)
	cookie, token := getCSRF(t, supervisor)
	body := validCompleteBody(t)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := newJSONRequest(
		http.MethodPost,
		SetupCompletePath,
		body,
		cookie,
		token,
	).WithContext(requestContext)
	request.Header.Set("X-Request-ID", "setup-request-1")
	recorder := httptest.NewRecorder()
	supervisor.ServeHTTP(recorder, request)
	assertStatus(t, recorder, http.StatusAccepted)
	cancelRequest()

	select {
	case <-initializerStarted:
	case <-time.After(time.Second):
		t.Fatal("initializer did not start after 202 response")
	}
	metadata := <-metadataReceived
	if metadata.RequestID != "setup-request-1" || metadata.IPAddress != "192.0.2.1" {
		t.Fatalf("metadata = %#v", metadata)
	}

	var installation Installation
	select {
	case installation = <-storeStarted:
	case <-time.After(time.Second):
		t.Fatal("installation store was not reached")
	}
	if installation.Version != InstallationVersion ||
		installation.Source != "setup" ||
		installation.Database.DSN != testDSN ||
		installation.SessionSecret != testSessionSecret ||
		installation.InstalledAt.IsZero() {
		t.Fatalf("installation = %#v", installation)
	}
	if supervisor.Mode() != ModeSetup {
		t.Fatalf("mode before store commit = %q", supervisor.Mode())
	}
	status := supervisor.Status()
	if status.Status != StatusInitializing ||
		status.Stage != StagePersistingConfiguration {
		t.Fatalf("status during commit = %#v", status)
	}

	close(releaseStore)
	waitForMode(t, supervisor, ModeApplication)

	application := performRequest(
		supervisor,
		http.MethodGet,
		"/api/v1/application-marker",
		nil,
	)
	assertStatus(t, application, http.StatusOK)
	if !strings.Contains(application.Body.String(), `"application":true`) {
		t.Fatalf("application response = %s", application.Body.String())
	}

	mode := performRequest(supervisor, http.MethodGet, SystemModePath, nil)
	var modeResponse SystemModeResponse
	decodeResponse(t, mode, &modeResponse)
	if modeResponse.Mode != ModeApplication {
		t.Fatalf("mode response = %#v", modeResponse)
	}
	closedSetup := performRequest(supervisor, http.MethodGet, SetupStatusPath, nil)
	assertProblem(t, closedSetup, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertNoStore(t, closedSetup)

	shutdownSupervisor(t, supervisor)
	select {
	case <-shutdownCalled:
	case <-time.After(time.Second):
		t.Fatal("activated candidate was not shut down")
	}
}

func TestCompleteRejectsConcurrentAttempt(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			ctx context.Context,
			_ SetupCompleteRequest,
			_ ProgressReporter,
		) (Candidate, error) {
			startOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return Candidate{}, ctx.Err()
			}
			return validCandidate(), nil
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	body := validCompleteBody(t)

	first := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		body,
		cookie,
		token,
	)
	assertStatus(t, first, http.StatusAccepted)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first initializer did not start")
	}

	second := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		body,
		cookie,
		token,
	)
	assertProblem(t, second, http.StatusConflict, "SETUP_IN_PROGRESS")
	if strings.Contains(second.Body.String(), testDSN) ||
		strings.Contains(second.Body.String(), testPassword) {
		t.Fatalf("concurrency error leaked setup input: %s", second.Body.String())
	}

	close(release)
	waitForMode(t, supervisor, ModeApplication)
}

func TestProgressReportsCannotMoveBackward(t *testing.T) {
	supervisor := &Supervisor{
		attempt: attemptState{
			status:     StatusInitializing,
			stage:      StageMigrating,
			generation: 7,
		},
	}

	supervisor.reportStage(7, StageValidatingDatabase)
	if got := supervisor.Status().Stage; got != StageMigrating {
		t.Fatalf("stage moved backward to %q", got)
	}

	supervisor.reportStage(7, StageBootstrapping)
	if got := supervisor.Status().Stage; got != StageBootstrapping {
		t.Fatalf("stage = %q, want %q", got, StageBootstrapping)
	}
}

func TestApplicationModePermanentlyHidesSetupRoutes(t *testing.T) {
	application := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(request.URL.Path))
	})
	supervisor := newTestSupervisor(t, Config{
		Mode:        ModeApplication,
		Application: application,
	})

	regular := performRequest(supervisor, http.MethodGet, "/api/v1/users", nil)
	assertStatus(t, regular, http.StatusCreated)
	for _, path := range []string{
		"/api/v1/setup",
		SetupStatusPath,
		SetupCompletePath,
		"/api/v1/setup-malicious",
	} {
		response := performRequest(supervisor, http.MethodGet, path, nil)
		assertProblem(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		assertNoStore(t, response)
	}

	mode := performRequest(supervisor, http.MethodGet, SystemModePath, nil)
	assertStatus(t, mode, http.StatusOK)
	var result SystemModeResponse
	decodeResponse(t, mode, &result)
	if result.Mode != ModeApplication {
		t.Fatalf("mode = %q", result.Mode)
	}
}

func TestInitializationFailuresStayInSetupAndCanBeRetried(t *testing.T) {
	tests := []struct {
		name         string
		configure    func(*Config, *atomic.Int32)
		wantStage    Stage
		wantCode     string
		wantCleanups int32
	}{
		{
			name: "database validation",
			configure: func(config *Config, _ *atomic.Int32) {
				config.DatabaseTester = DatabaseTesterFunc(func(
					context.Context,
					DatabaseConfig,
				) error {
					return errors.New("secret database failure " + testDSN)
				})
			},
			wantStage: StageValidatingDatabase,
			wantCode:  "SETUP_DATABASE_UNAVAILABLE",
		},
		{
			name: "application initialization",
			configure: func(config *Config, cleanups *atomic.Int32) {
				config.Initializer = ApplicationInitializerFunc(func(
					context.Context,
					SetupCompleteRequest,
					ProgressReporter,
				) (Candidate, error) {
					candidate := validCandidate()
					candidate.Shutdown = func(context.Context) error {
						cleanups.Add(1)
						return nil
					}
					return candidate, errors.New("secret migration SQL")
				})
			},
			wantStage:    StageMigrating,
			wantCode:     "SETUP_APPLICATION_INITIALIZATION_FAILED",
			wantCleanups: 2,
		},
		{
			name: "invalid candidate",
			configure: func(config *Config, cleanups *atomic.Int32) {
				config.Initializer = ApplicationInitializerFunc(func(
					context.Context,
					SetupCompleteRequest,
					ProgressReporter,
				) (Candidate, error) {
					return Candidate{
						SessionSecret: testSessionSecret,
						Shutdown: func(context.Context) error {
							cleanups.Add(1)
							return nil
						},
					}, nil
				})
			},
			wantStage:    StageMigrating,
			wantCode:     "SETUP_APPLICATION_INITIALIZATION_FAILED",
			wantCleanups: 2,
		},
		{
			name: "configuration persistence",
			configure: func(config *Config, cleanups *atomic.Int32) {
				config.Initializer = ApplicationInitializerFunc(func(
					context.Context,
					SetupCompleteRequest,
					ProgressReporter,
				) (Candidate, error) {
					candidate := validCandidate()
					candidate.Shutdown = func(context.Context) error {
						cleanups.Add(1)
						return nil
					}
					return candidate, nil
				})
				config.Store = InstallationStoreFunc(func(
					context.Context,
					Installation,
				) error {
					return errors.New("secret config path")
				})
			},
			wantStage:    StagePersistingConfiguration,
			wantCode:     "SETUP_CONFIGURATION_COMMIT_FAILED",
			wantCleanups: 2,
		},
		{
			name: "initializer panic",
			configure: func(config *Config, _ *atomic.Int32) {
				config.Initializer = ApplicationInitializerFunc(func(
					context.Context,
					SetupCompleteRequest,
					ProgressReporter,
				) (Candidate, error) {
					panic("secret panic " + testPassword)
				})
			},
			wantStage: StageMigrating,
			wantCode:  "SETUP_APPLICATION_INITIALIZATION_FAILED",
		},
		{
			name: "installation store panic",
			configure: func(config *Config, cleanups *atomic.Int32) {
				config.Initializer = ApplicationInitializerFunc(func(
					context.Context,
					SetupCompleteRequest,
					ProgressReporter,
				) (Candidate, error) {
					candidate := validCandidate()
					candidate.Shutdown = func(context.Context) error {
						cleanups.Add(1)
						return nil
					}
					return candidate, nil
				})
				config.Store = InstallationStoreFunc(func(
					context.Context,
					Installation,
				) error {
					panic("secret persistence panic")
				})
			},
			wantStage:    StagePersistingConfiguration,
			wantCode:     "SETUP_CONFIGURATION_COMMIT_FAILED",
			wantCleanups: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var cleanups atomic.Int32
			config := Config{Mode: ModeSetup}
			test.configure(&config, &cleanups)
			supervisor := newTestSupervisor(t, config)
			cookie, token := getCSRF(t, supervisor)

			for attempt := 0; attempt < 2; attempt++ {
				accepted := performJSONRequest(
					supervisor,
					http.MethodPost,
					SetupCompletePath,
					validCompleteBody(t),
					cookie,
					token,
				)
				assertStatus(t, accepted, http.StatusAccepted)
				status := waitForStatus(t, supervisor, StatusFailed)
				if status.Stage != test.wantStage || status.Code != test.wantCode {
					t.Fatalf("failed status = %#v", status)
				}
				statusResponse := performRequest(
					supervisor,
					http.MethodGet,
					SetupStatusPath,
					nil,
				)
				if strings.Contains(statusResponse.Body.String(), testDSN) ||
					strings.Contains(statusResponse.Body.String(), testPassword) ||
					strings.Contains(statusResponse.Body.String(), "secret") {
					t.Fatalf("failure status leaked details: %s", statusResponse.Body.String())
				}
			}
			if supervisor.Mode() != ModeSetup {
				t.Fatalf("failed initialization changed mode to %q", supervisor.Mode())
			}
			if cleanups.Load() != test.wantCleanups {
				t.Fatalf("cleanup calls = %d, want %d", cleanups.Load(), test.wantCleanups)
			}
		})
	}
}

func TestSealedInstallationFailurePermanentlyClosesSetup(t *testing.T) {
	var cleanups atomic.Int32
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			candidate := validCandidate()
			candidate.Shutdown = func(context.Context) error {
				cleanups.Add(1)
				return nil
			}
			return candidate, nil
		}),
		Store: InstallationStoreFunc(func(
			context.Context,
			Installation,
		) error {
			return ErrInstallationSealed
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	waitForMode(t, supervisor, ModeApplication)

	closed := performRequest(supervisor, http.MethodGet, SetupStatusPath, nil)
	assertProblem(t, closed, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertNoStore(t, closed)
	unavailable := performRequest(supervisor, http.MethodGet, "/health/ready", nil)
	assertProblem(t, unavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
	assertNoStore(t, unavailable)

	deadline := time.Now().Add(time.Second)
	for cleanups.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cleanups.Load() != 1 {
		t.Fatalf("candidate cleanup calls = %d, want 1", cleanups.Load())
	}
}

func TestShutdownWaitsForSealedCleanupAndRetryObservesSameCleanup(
	t *testing.T,
) {
	cleanupStarted := make(chan context.Context, 1)
	releaseCleanup := make(chan struct{})
	cleanupReturned := make(chan struct{})
	var releaseOnce sync.Once
	var cleanupCalls atomic.Int32

	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			candidate := validCandidate()
			candidate.Shutdown = func(ctx context.Context) error {
				cleanupCalls.Add(1)
				cleanupStarted <- ctx
				<-releaseCleanup
				close(cleanupReturned)
				return nil
			}
			return candidate, nil
		}),
		Store: InstallationStoreFunc(func(
			context.Context,
			Installation,
		) error {
			return ErrInstallationSealed
		}),
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
	})

	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	waitForMode(t, supervisor, ModeApplication)

	var cleanupContext context.Context
	select {
	case cleanupContext = <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("sealed candidate cleanup did not start")
	}
	if _, hasDeadline := cleanupContext.Deadline(); !hasDeadline {
		t.Fatal("sealed candidate cleanup context is not bounded")
	}
	closed := performRequest(supervisor, http.MethodGet, SetupStatusPath, nil)
	assertProblem(t, closed, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertNoStore(t, closed)

	firstContext, cancelFirst := context.WithTimeout(
		context.Background(),
		20*time.Millisecond,
	)
	firstErr := supervisor.Shutdown(firstContext)
	cancelFirst()
	if !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("shutdown during sealed cleanup = %v, want deadline exceeded", firstErr)
	}
	select {
	case <-cleanupReturned:
		t.Fatal("sealed cleanup returned before it was released")
	default:
	}

	retryContext, cancelRetry := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- supervisor.Shutdown(retryContext)
	}()
	select {
	case err := <-retryDone:
		t.Fatalf("retry shutdown skipped sealed cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseCleanup) })
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("retry shutdown after cleanup release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry shutdown did not observe completed sealed cleanup")
	}
	cancelRetry()

	if cleanupCalls.Load() != 1 {
		t.Fatalf("sealed candidate cleanup calls = %d, want 1", cleanupCalls.Load())
	}
	if err := supervisor.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown after sealed cleanup completed: %v", err)
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("sealed candidate cleaned more than once: %d", cleanupCalls.Load())
	}
}

func TestCompleteValidatesAdministratorBeforeStarting(t *testing.T) {
	var initializations atomic.Int32
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			initializations.Add(1)
			return validCandidate(), nil
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	tests := []SetupCompleteInput{
		{
			Database: DatabaseInput{Driver: "oracle"},
			Administrator: AdministratorConfig{
				Email: "admin@example.com", Password: testPassword,
			},
		},
		{
			Database: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{
					Directory: " data",
					Filename:  "db.sqlite",
				},
			},
			Administrator: AdministratorConfig{
				Email: "admin@example.com", Password: testPassword,
			},
		},
		{
			Database: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{
					Directory: "data",
					Filename:  "db.sqlite",
				},
			},
			Administrator: AdministratorConfig{
				Email: "Admin <admin@example.com>", Password: testPassword,
			},
		},
		{
			Database: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{
					Directory: "data",
					Filename:  "db.sqlite",
				},
			},
			Administrator: AdministratorConfig{
				Email: "admin@example.com", Password: "passwordpassword",
			},
		},
		{
			Database: testPostgresInput(),
			Administrator: AdministratorConfig{
				Email: "admin@example.com", Password: "",
			},
		},
	}
	for _, request := range tests {
		response := performJSONRequest(
			supervisor,
			http.MethodPost,
			SetupCompletePath,
			marshalJSON(t, request),
			cookie,
			token,
		)
		assertProblem(t, response, http.StatusBadRequest, "REQUEST_INVALID")
	}
	if initializations.Load() != 0 {
		t.Fatalf("initializations = %d", initializations.Load())
	}
}

func TestCompleteAcceptsAdministratorPasswordsWithoutLengthBounds(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "single character", password: "x"},
		{name: "six characters", password: "123456"},
		{
			name:     "longer than former limit",
			password: "123456" + strings.Repeat("z", 2048),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan string, 1)
			supervisor := newTestSupervisor(t, Config{
				Mode: ModeSetup,
				Initializer: ApplicationInitializerFunc(func(
					_ context.Context,
					request SetupCompleteRequest,
					_ ProgressReporter,
				) (Candidate, error) {
					received <- request.Administrator.Password
					return validCandidate(), nil
				}),
			})
			cookie, token := getCSRF(t, supervisor)
			response := performJSONRequest(
				supervisor,
				http.MethodPost,
				SetupCompletePath,
				marshalJSON(t, SetupCompleteInput{
					Database: testPostgresInput(),
					Administrator: AdministratorConfig{
						Email:    "admin@example.com",
						Password: test.password,
					},
				}),
				cookie,
				token,
			)
			assertStatus(t, response, http.StatusAccepted)

			select {
			case password := <-received:
				if password != test.password {
					t.Fatalf("initializer password length = %d, want %d", len(password), len(test.password))
				}
			case <-time.After(time.Second):
				t.Fatal("initializer did not receive administrator password")
			}
		})
	}
}

func TestShutdownCancelsDetachedInitialization(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			ctx context.Context,
			_ SetupCompleteRequest,
			_ ProgressReporter,
		) (Candidate, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return Candidate{}, ctx.Err()
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initializer did not start")
	}
	shutdownSupervisor(t, supervisor)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("initializer context was not canceled")
	}
	status := supervisor.Status()
	if status.Status != StatusFailed {
		t.Fatalf("status after shutdown = %#v", status)
	}
}

func TestActiveShutdownIsSharedAcrossConcurrentCallsAndTimeoutRetry(
	t *testing.T,
) {
	cleanupStarted := make(chan context.Context, 1)
	releaseCleanup := make(chan struct{})
	cleanupReturned := make(chan struct{})
	var releaseOnce sync.Once
	var cleanupCalls atomic.Int32

	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			candidate := validCandidate()
			candidate.Shutdown = func(ctx context.Context) error {
				cleanupCalls.Add(1)
				cleanupStarted <- ctx
				<-releaseCleanup
				close(cleanupReturned)
				return nil
			}
			return candidate, nil
		}),
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
	})

	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	waitForMode(t, supervisor, ModeApplication)

	firstContext, cancelFirst := context.WithTimeout(
		context.Background(),
		50*time.Millisecond,
	)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- supervisor.Shutdown(firstContext)
	}()

	var cleanupContext context.Context
	select {
	case cleanupContext = <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("active candidate cleanup did not start")
	}
	if _, hasDeadline := cleanupContext.Deadline(); !hasDeadline {
		t.Fatal("active candidate cleanup context is not bounded")
	}

	concurrentContext, cancelConcurrent := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	concurrentDone := make(chan error, 1)
	go func() {
		concurrentDone <- supervisor.Shutdown(concurrentContext)
	}()

	select {
	case err := <-firstDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first shutdown = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first shutdown did not observe its deadline")
	}
	cancelFirst()
	if err := cleanupContext.Err(); err != nil {
		t.Fatalf("caller deadline canceled active cleanup context: %v", err)
	}

	retryContext, cancelRetry := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- supervisor.Shutdown(retryContext)
	}()

	select {
	case err := <-concurrentDone:
		t.Fatalf("concurrent shutdown skipped active cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case err := <-retryDone:
		t.Fatalf("retry shutdown skipped active cleanup: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseCleanup) })
	select {
	case <-cleanupReturned:
	case <-time.After(time.Second):
		t.Fatal("active candidate cleanup did not return")
	}
	select {
	case err := <-concurrentDone:
		if err != nil {
			t.Fatalf("concurrent shutdown after cleanup release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent shutdown did not observe completed cleanup")
	}
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("retry shutdown after cleanup release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry shutdown did not observe completed cleanup")
	}
	cancelConcurrent()
	cancelRetry()

	if err := supervisor.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown after active cleanup completed: %v", err)
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("active candidate cleanup calls = %d, want 1", cleanupCalls.Load())
	}
}

func TestActiveShutdownErrorIsReplayedToEveryCaller(t *testing.T) {
	cleanupStarted := make(chan struct{}, 1)
	releaseCleanup := make(chan struct{})
	var releaseOnce sync.Once
	var cleanupCalls atomic.Int32

	supervisor := buildTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			candidate := validCandidate()
			candidate.Shutdown = func(context.Context) error {
				cleanupCalls.Add(1)
				cleanupStarted <- struct{}{}
				<-releaseCleanup
				return errors.New("secret database shutdown detail")
			}
			return candidate, nil
		}),
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = supervisor.Shutdown(ctx)
	})

	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	waitForMode(t, supervisor, ModeApplication)

	const callerCount = 4
	startCallers := make(chan struct{})
	callersReady := make(chan struct{}, callerCount)
	results := make(chan error, callerCount)
	for range callerCount {
		go func() {
			callersReady <- struct{}{}
			<-startCallers
			results <- supervisor.Shutdown(context.Background())
		}()
	}
	for range callerCount {
		<-callersReady
	}
	close(startCallers)
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("active candidate cleanup did not start")
	}
	releaseOnce.Do(func() { close(releaseCleanup) })

	var firstResult error
	for range callerCount {
		select {
		case result := <-results:
			if result == nil || result.Error() != "activated application shutdown failed" {
				t.Fatalf("shutdown result = %v, want sanitized cleanup error", result)
			}
			if firstResult == nil {
				firstResult = result
			} else if result != firstResult {
				t.Fatalf("shutdown callers observed different cleanup errors: %p != %p", result, firstResult)
			}
		case <-time.After(time.Second):
			t.Fatal("shutdown caller did not observe cleanup result")
		}
	}

	laterResult := supervisor.Shutdown(context.Background())
	if laterResult != firstResult {
		t.Fatalf("later shutdown result = %p, want stable result %p", laterResult, firstResult)
	}
	if strings.Contains(laterResult.Error(), "secret") {
		t.Fatalf("shutdown result leaked dependency error: %v", laterResult)
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("active candidate cleanup calls = %d, want 1", cleanupCalls.Load())
	}
}

func TestCommitFinishingAfterShutdownTimeoutSealsAndCleansCandidate(
	t *testing.T,
) {
	storeStarted := make(chan struct{})
	releaseStore := make(chan struct{})
	cleanupDone := make(chan struct{})
	var cleanupCalls atomic.Int32
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			candidate := validCandidate()
			candidate.Shutdown = func(context.Context) error {
				if cleanupCalls.Add(1) == 1 {
					close(cleanupDone)
				}
				return nil
			}
			return candidate, nil
		}),
		Store: InstallationStoreFunc(func(
			context.Context,
			Installation,
		) error {
			close(storeStarted)
			<-releaseStore
			return nil
		}),
	})
	cookie, token := getCSRF(t, supervisor)
	accepted := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupCompletePath,
		validCompleteBody(t),
		cookie,
		token,
	)
	assertStatus(t, accepted, http.StatusAccepted)
	select {
	case <-storeStarted:
	case <-time.After(time.Second):
		t.Fatal("installation store did not start")
	}

	timedOutContext, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(-time.Second),
	)
	err := supervisor.Shutdown(timedOutContext)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}

	close(releaseStore)
	waitForMode(t, supervisor, ModeApplication)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("post-commit candidate was not cleaned up")
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("candidate cleanup calls = %d, want 1", cleanupCalls.Load())
	}

	closedSetup := performRequest(
		supervisor,
		http.MethodGet,
		SetupStatusPath,
		nil,
	)
	assertProblem(t, closedSetup, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertNoStore(t, closedSetup)

	unavailable := performRequest(
		supervisor,
		http.MethodGet,
		"/api/v1/application-marker",
		nil,
	)
	assertProblem(
		t,
		unavailable,
		http.StatusServiceUnavailable,
		"SERVICE_UNAVAILABLE",
	)
	assertNoStore(t, unavailable)

	if err := supervisor.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeat shutdown after cleanup: %v", err)
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("candidate cleaned more than once: %d", cleanupCalls.Load())
	}
}

func TestSetupWriteRateLimitIsProcessLocalAndBounded(t *testing.T) {
	supervisor := newTestSupervisor(t, Config{
		Mode: ModeSetup,
		RateLimit: RateLimitConfig{
			Limit:      1,
			Window:     time.Minute,
			MaxEntries: 2,
		},
	})
	cookie, token := getCSRF(t, supervisor)
	body := marshalJSON(t, SetupDatabaseTestRequest{
		Database: DatabaseInput{
			Driver: "sqlite",
			SQLite: &SQLiteDatabaseInput{
				Directory: "data",
				Filename:  "db.sqlite",
			},
		},
	})
	first := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		cookie,
		token,
	)
	assertStatus(t, first, http.StatusOK)
	second := performJSONRequest(
		supervisor,
		http.MethodPost,
		SetupDatabaseTestPath,
		body,
		cookie,
		token,
	)
	assertProblem(t, second, http.StatusTooManyRequests, "RATE_LIMITED")
	if second.Header().Get("Retry-After") == "" ||
		second.Header().Get("RateLimit-Limit") != "1" {
		t.Fatalf("rate-limit headers = %#v", second.Header())
	}

	now := time.Unix(1000, 0)
	limiter := newMemoryRateLimiter(RateLimitConfig{
		Limit: 1, Window: time.Minute, MaxEntries: 1,
	}, func() time.Time { return now })
	if !limiter.consume("first").allowed {
		t.Fatal("first key was unexpectedly denied")
	}
	if limiter.consume("second").allowed {
		t.Fatal("entry cap did not deny a new key")
	}
	now = now.Add(time.Minute)
	if !limiter.consume("second").allowed {
		t.Fatal("expired entry was not pruned")
	}
}

func TestNewValidatesModeAndDependencies(t *testing.T) {
	validHTTP := func() Config {
		return Config{AllowedOrigins: []string{testOrigin}}
	}
	tests := []struct {
		name   string
		config Config
	}{
		{name: "unknown mode", config: validHTTP()},
		{name: "missing origins", config: Config{Mode: ModeApplication, Application: http.NotFoundHandler()}},
		{name: "missing application", config: func() Config {
			config := validHTTP()
			config.Mode = ModeApplication
			return config
		}()},
		{name: "missing setup dependencies", config: func() Config {
			config := validHTTP()
			config.Mode = ModeSetup
			return config
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config); err == nil {
				t.Fatal("New unexpectedly succeeded")
			}
		})
	}
}

func newTestSupervisor(t *testing.T, override Config) *Supervisor {
	t.Helper()
	supervisor := buildTestSupervisor(t, override)
	t.Cleanup(func() {
		shutdownSupervisor(t, supervisor)
	})
	return supervisor
}

func buildTestSupervisor(t *testing.T, override Config) *Supervisor {
	t.Helper()
	gin.SetMode(gin.TestMode)
	config := Config{
		Mode:           override.Mode,
		Application:    override.Application,
		AllowedOrigins: []string{testOrigin},
		TrustedProxies: nil,
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   16 << 10,
			MaxHeaderBytes: 16 << 10,
			MaxHeaderCount: 64,
		},
		CSRFCookieSameSite:    http.SameSiteLaxMode,
		CSRFTokenTTL:          time.Hour,
		DatabaseTestTimeout:   time.Second,
		InitializationTimeout: 2 * time.Second,
		CleanupTimeout:        time.Second,
		RateLimit: RateLimitConfig{
			Limit:      100,
			Window:     time.Minute,
			MaxEntries: 128,
		},
		DatabaseTester: DatabaseTesterFunc(func(
			context.Context,
			DatabaseConfig,
		) error {
			return nil
		}),
		Initializer: ApplicationInitializerFunc(func(
			context.Context,
			SetupCompleteRequest,
			ProgressReporter,
		) (Candidate, error) {
			return validCandidate(), nil
		}),
		Store: InstallationStoreFunc(func(
			context.Context,
			Installation,
		) error {
			return nil
		}),
	}
	mergeTestConfig(&config, override)
	supervisor, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func mergeTestConfig(target *Config, override Config) {
	if override.Application != nil {
		target.Application = override.Application
	}
	if override.DatabaseTester != nil {
		target.DatabaseTester = override.DatabaseTester
	}
	if override.Initializer != nil {
		target.Initializer = override.Initializer
	}
	if override.Store != nil {
		target.Store = override.Store
	}
	if override.RequestLimits != (httpx.RequestLimits{}) {
		target.RequestLimits = override.RequestLimits
	}
	if override.RateLimit != (RateLimitConfig{}) {
		target.RateLimit = override.RateLimit
	}
}

func validCandidate() Candidate {
	return Candidate{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		SessionSecret: testSessionSecret,
		Shutdown: func(context.Context) error {
			return nil
		},
	}
}

func validCompleteBody(t *testing.T) []byte {
	t.Helper()
	return marshalJSON(t, SetupCompleteInput{
		Database: testPostgresInput(),
		Administrator: AdministratorConfig{
			Email:    "admin@example.com",
			Password: testPassword,
		},
	})
}

func testPostgresInput() DatabaseInput {
	return DatabaseInput{
		Driver: "postgres",
		Postgres: &PostgresDatabaseInput{
			Host:     "db.example",
			Port:     5432,
			Database: "aginex",
			Username: "aginex",
			Password: "database-secret",
			SSLMode:  "require",
		},
	}
}

func getCSRF(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	response := performRequestWithHeaders(
		handler,
		http.MethodGet,
		"/api/v1/auth/csrf",
		nil,
		map[string]string{"Origin": testOrigin},
		nil,
	)
	assertStatus(t, response, http.StatusOK)
	var payload csrfTokenResponse
	decodeResponse(t, response, &payload)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == httpx.CSRFCookieName {
			return cookie, payload.Token
		}
	}
	t.Fatal("CSRF cookie missing")
	return nil, ""
}

func performJSONRequest(
	handler http.Handler,
	method string,
	path string,
	body []byte,
	cookie *http.Cookie,
	token string,
) *httptest.ResponseRecorder {
	request := newJSONRequest(method, path, body, cookie, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newJSONRequest(
	method string,
	path string,
	body []byte,
	cookie *http.Cookie,
	token string,
) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testOrigin)
	if token != "" {
		request.Header.Set(httpx.CSRFHeaderName, token)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func performRequest(
	handler http.Handler,
	method string,
	path string,
	body *bytes.Reader,
) *httptest.ResponseRecorder {
	return performRequestWithHeaders(handler, method, path, body, nil, nil)
}

func performRequestWithHeaders(
	handler http.Handler,
	method string,
	path string,
	body *bytes.Reader,
	headers map[string]string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	request := httptest.NewRequest(method, path, reader)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
}

func assertProblem(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	assertStatus(t, response, status)
	if mediaType := response.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, httpx.ProblemMediaType) {
		t.Fatalf("content type = %q", mediaType)
	}
	var problem httpx.Problem
	decodeResponse(t, response, &problem)
	if problem.Code != code || problem.Status != status {
		t.Fatalf("problem = %#v", problem)
	}
}

func assertNoStore(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", response.Header().Get("Cache-Control"))
	}
}

func waitForMode(t *testing.T, supervisor *Supervisor, mode Mode) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if supervisor.Mode() == mode {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("mode = %q, want %q; status = %#v", supervisor.Mode(), mode, supervisor.Status())
}

func waitForStatus(t *testing.T, supervisor *Supervisor, status Status) SetupStatusResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current := supervisor.Status()
		if current.Status == status {
			return current
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status = %#v, want %q", supervisor.Status(), status)
	return SetupStatusResponse{}
}

func shutdownSupervisor(t *testing.T, supervisor *Supervisor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Shutdown(ctx); err != nil {
		t.Errorf("shutdown supervisor: %v", err)
	}
}
