package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/internal/config"
	"gorm.io/gorm"
)

var _ func(Definition, context.Context) error = Definition.RunWorker

func TestRunWorkerRejectsInvalidContextBeforeLoadingConfiguration(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.RunWorker(nil); err == nil ||
		!strings.Contains(err.Error(), "context is required") {
		t.Fatalf("nil context error = %v", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := definition.RunWorker(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context cancellation", err)
	}
}

func TestWaitForWorkerAPIInitializationRequiresDurableReadyApplication(
	t *testing.T,
) {
	var loads atomic.Int32
	var modeProbes atomic.Int32
	var readyProbes atomic.Int32
	configured := configuredWorkerHostState("http://api.example.test")
	configured.Config.Jobs.WorkerID = "released-worker"

	client := &http.Client{Transport: workerHostRoundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/system/mode":
			if modeProbes.Add(1) == 1 {
				return workerHostTestResponse(
					http.StatusOK,
					`{"mode":"setup"}`,
				), nil
			}
			return workerHostTestResponse(
				http.StatusOK,
				`{"mode":"application"}`,
			), nil
		case "/health/ready":
			if readyProbes.Add(1) == 1 {
				return workerHostTestResponse(http.StatusServiceUnavailable, ""), nil
			}
			return workerHostTestResponse(http.StatusOK, ""), nil
		default:
			t.Fatalf("unexpected startup probe %s", request.URL.Path)
			return nil, errors.New("unexpected startup probe")
		}
	})}

	cfg, err := waitForWorkerAPIInitialization(
		t.Context(),
		func() (config.State, error) {
			switch loads.Add(1) {
			case 1:
				return config.State{Status: config.StatusSetup}, nil
			case 2:
				pending := configured
				pending.NeedsEnvironmentMarker = true
				return pending, nil
			default:
				return configured, nil
			}
		},
		client,
		time.Microsecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jobs.WorkerID != "released-worker" {
		t.Fatalf("released configuration = %#v", cfg.Jobs)
	}
	if got := loads.Load(); got != 5 {
		t.Fatalf("installation state loads = %d, want 5", got)
	}
	if got := modeProbes.Load(); got != 3 {
		t.Fatalf("mode probes = %d, want 3", got)
	}
	if got := readyProbes.Load(); got != 2 {
		t.Fatalf("readiness probes = %d, want 2", got)
	}
}

func TestWaitForWorkerAPIInitializationFailsClosedOnInvalidState(
	t *testing.T,
) {
	var probes atomic.Int32
	client := &http.Client{Transport: workerHostRoundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		probes.Add(1)
		return workerHostTestResponse(http.StatusOK, `{"mode":"application"}`), nil
	})}

	_, err := waitForWorkerAPIInitialization(
		t.Context(),
		func() (config.State, error) {
			return config.State{
				Status: config.StatusConfigured,
				Config: config.Config{HTTP: config.HTTP{
					PublicURL: "http://api.example.test",
				}},
			}, nil
		},
		client,
		time.Hour,
	)
	if !errors.Is(err, errInvalidWorkerHostStartupConfiguration) {
		t.Fatalf("error = %v, want invalid configuration", err)
	}
	if got := probes.Load(); got != 0 {
		t.Fatalf("API probes = %d before durable marker, want 0", got)
	}
}

func TestWorkerHostProbesAreBoundedAndStrict(t *testing.T) {
	client := newWorkerHostHTTPClient()
	if client.Timeout != workerHostStartupRequestTimeout {
		t.Fatalf(
			"startup request timeout = %v, want %v",
			client.Timeout,
			workerHostStartupRequestTimeout,
		)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error = %v, want http.ErrUseLastResponse", err)
	}

	for _, body := range []string{
		`{"mode":"application","extra":true}`,
		`{"mode":"application"} {}`,
		`{"mode":"application"}` + strings.Repeat(" ", workerHostStartupResponseLimit),
	} {
		var readyCalls atomic.Int32
		probeClient := &http.Client{Transport: workerHostRoundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			if request.URL.Path == "/health/ready" {
				readyCalls.Add(1)
				return workerHostTestResponse(http.StatusOK, ""), nil
			}
			return workerHostTestResponse(http.StatusOK, body), nil
		})}
		ready, err := workerHostApplicationReady(
			t.Context(),
			probeClient,
			"https://api.example.test",
		)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			t.Fatalf("invalid mode payload released worker: %q", body)
		}
		if got := readyCalls.Load(); got != 0 {
			t.Fatalf("readiness probes = %d after invalid mode payload", got)
		}
	}

	const secret = "worker-url-secret"
	_, err := workerHostApplicationReady(
		t.Context(),
		&http.Client{},
		"https://worker:"+secret+"@api.example.test",
	)
	if !errors.Is(err, errInvalidWorkerHostStartupConfiguration) {
		t.Fatalf("credentialed URL error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credentialed URL leaked into error: %v", err)
	}
}

func TestRunWorkerHostOrdersReadinessLifecycleAndDatabaseClose(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	state := configuredWorkerHostState("http://api.example.test")
	state.Config.HTTP.ShutdownGracePeriod = 50 * time.Millisecond
	events := make([]string, 0, 10)
	databaseHandle := &gorm.DB{}
	runtime := &testWorkerHostRuntime{
		start: func(context.Context) error {
			events = append(events, "start")
			return nil
		},
		ready: func(context.Context) error {
			events = append(events, "worker-ready")
			return nil
		},
		run: func(context.Context) error {
			events = append(events, "run")
			return nil
		},
		shutdown: func(ctx context.Context) error {
			events = append(events, "shutdown")
			if err := ctx.Err(); err != nil {
				t.Fatalf("shutdown context is already done: %v", err)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("shutdown context has no deadline")
			}
			return nil
		},
	}

	err = runWorkerHost(t.Context(), definition, workerHostDependencies{
		loadState: func() (config.State, error) {
			events = append(events, "load")
			return state, nil
		},
		client: &http.Client{Transport: workerHostRoundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			switch request.URL.Path {
			case "/api/v1/system/mode":
				events = append(events, "api-mode")
				return workerHostTestResponse(
					http.StatusOK,
					`{"mode":"application"}`,
				), nil
			case "/health/ready":
				events = append(events, "api-ready")
				return workerHostTestResponse(http.StatusOK, ""), nil
			default:
				return workerHostTestResponse(http.StatusNotFound, ""), nil
			}
		})},
		pollInterval: time.Hour,
		openDatabase: func(
			context.Context,
			config.Database,
		) (workerHostDatabase, error) {
			events = append(events, "open")
			return workerHostDatabase{
				database: databaseHandle,
				close: func() error {
					events = append(events, "close")
					return nil
				},
			}, nil
		},
		newRuntime: func(
			_ context.Context,
			_ config.Config,
			db *gorm.DB,
		) (workerHostRuntime, error) {
			events = append(events, "create")
			if db != databaseHandle {
				t.Fatalf("worker database = %p, want %p", db, databaseHandle)
			}
			return runtime, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"load",
		"api-mode",
		"api-ready",
		"open",
		"create",
		"start",
		"worker-ready",
		"run",
		"shutdown",
		"close",
	}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("worker host events = %v, want %v", events, want)
	}
}

func TestRunWorkerHostJoinsReadinessShutdownAndCloseErrors(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	readyErr := errors.New("readiness failed")
	shutdownErr := errors.New("shutdown failed")
	closeErr := errors.New("database close failed")
	ctx, cancel := context.WithCancel(t.Context())
	events := make([]string, 0, 5)

	err = runWorkerHost(ctx, definition, readyWorkerHostDependencies(
		configuredWorkerHostState("http://api.example.test"),
		&testWorkerHostRuntime{
			start: func(context.Context) error {
				events = append(events, "start")
				return nil
			},
			ready: func(context.Context) error {
				events = append(events, "ready")
				cancel()
				return readyErr
			},
			shutdown: func(shutdownContext context.Context) error {
				events = append(events, "shutdown")
				if err := shutdownContext.Err(); err != nil {
					t.Fatalf("shutdown inherited canceled context: %v", err)
				}
				return shutdownErr
			},
		},
		func() error {
			events = append(events, "close")
			return closeErr
		},
	))
	for _, target := range []error{readyErr, shutdownErr, closeErr} {
		if !errors.Is(err, target) {
			t.Fatalf("error = %v, want joined %v", err, target)
		}
	}
	if got := strings.Join(events, ","); got != "start,ready,shutdown,close" {
		t.Fatalf("failure lifecycle events = %s", got)
	}
}

func TestRunWorkerHostCancellationSemantics(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("waiting cancellation is graceful and never opens database", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var opens atomic.Int32
		err := runWorkerHost(ctx, definition, workerHostDependencies{
			loadState: func() (config.State, error) {
				cancel()
				return config.State{Status: config.StatusSetup}, nil
			},
			client:       &http.Client{},
			pollInterval: time.Hour,
			openDatabase: func(
				context.Context,
				config.Database,
			) (workerHostDatabase, error) {
				opens.Add(1)
				return workerHostDatabase{}, errors.New("must not open")
			},
			newRuntime: func(
				context.Context,
				config.Config,
				*gorm.DB,
			) (workerHostRuntime, error) {
				return nil, errors.New("must not create")
			},
		})
		if err != nil {
			t.Fatalf("waiting cancellation error = %v", err)
		}
		if got := opens.Load(); got != 0 {
			t.Fatalf("database opens = %d, want 0", got)
		}
	})

	t.Run("run cancellation is graceful", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var shutdowns atomic.Int32
		dependencies := readyWorkerHostDependencies(
			configuredWorkerHostState("http://api.example.test"),
			&testWorkerHostRuntime{
				run: func(ctx context.Context) error {
					cancel()
					return ctx.Err()
				},
				shutdown: func(context.Context) error {
					shutdowns.Add(1)
					return nil
				},
			},
			func() error { return nil },
		)
		if err := runWorkerHost(ctx, definition, dependencies); err != nil {
			t.Fatalf("run cancellation error = %v", err)
		}
		if got := shutdowns.Load(); got != 1 {
			t.Fatalf("shutdowns = %d, want 1", got)
		}
	})

	t.Run("run deadline is reported", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
		defer cancel()
		dependencies := readyWorkerHostDependencies(
			configuredWorkerHostState("http://api.example.test"),
			&testWorkerHostRuntime{
				run: func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				},
			},
			func() error { return nil },
		)
		err := runWorkerHost(ctx, definition, dependencies)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("run deadline error = %v", err)
		}
	})
}

func TestRunWorkerHostDoesNotSuppressOperationalCancellation(t *testing.T) {
	definition, err := Define()
	if err != nil {
		t.Fatal(err)
	}
	loadErr := fmt.Errorf("configuration backend failed: %w", context.Canceled)
	var opens atomic.Int32
	err = runWorkerHost(t.Context(), definition, workerHostDependencies{
		loadState: func() (config.State, error) {
			return config.State{}, loadErr
		},
		client:       &http.Client{},
		pollInterval: time.Hour,
		openDatabase: func(
			context.Context,
			config.Database,
		) (workerHostDatabase, error) {
			opens.Add(1)
			return workerHostDatabase{}, errors.New("must not open")
		},
		newRuntime: func(
			context.Context,
			config.Config,
			*gorm.DB,
		) (workerHostRuntime, error) {
			return nil, errors.New("must not create")
		},
	})
	if err == nil || !errors.Is(err, loadErr) {
		t.Fatalf("operational cancellation error = %v, want %v", err, loadErr)
	}
	if t.Context().Err() != nil {
		t.Fatalf("host context unexpectedly canceled: %v", t.Context().Err())
	}
	if got := opens.Load(); got != 0 {
		t.Fatalf("database opens = %d, want 0", got)
	}
}

type testWorkerHostRuntime struct {
	start    func(context.Context) error
	ready    func(context.Context) error
	run      func(context.Context) error
	shutdown func(context.Context) error
}

func (runtime *testWorkerHostRuntime) Start(ctx context.Context) error {
	if runtime.start == nil {
		return nil
	}
	return runtime.start(ctx)
}

func (runtime *testWorkerHostRuntime) Ready(ctx context.Context) error {
	if runtime.ready == nil {
		return nil
	}
	return runtime.ready(ctx)
}

func (runtime *testWorkerHostRuntime) Run(ctx context.Context) error {
	if runtime.run == nil {
		return nil
	}
	return runtime.run(ctx)
}

func (runtime *testWorkerHostRuntime) Shutdown(ctx context.Context) error {
	if runtime.shutdown == nil {
		return nil
	}
	return runtime.shutdown(ctx)
}

func readyWorkerHostDependencies(
	state config.State,
	runtime workerHostRuntime,
	closeDatabase func() error,
) workerHostDependencies {
	return workerHostDependencies{
		loadState: func() (config.State, error) { return state, nil },
		client: &http.Client{Transport: workerHostRoundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			if request.URL.Path == "/api/v1/system/mode" {
				return workerHostTestResponse(
					http.StatusOK,
					`{"mode":"application"}`,
				), nil
			}
			return workerHostTestResponse(http.StatusOK, ""), nil
		})},
		pollInterval: time.Hour,
		openDatabase: func(
			context.Context,
			config.Database,
		) (workerHostDatabase, error) {
			return workerHostDatabase{
				database: &gorm.DB{},
				close:    closeDatabase,
			}, nil
		},
		newRuntime: func(
			context.Context,
			config.Config,
			*gorm.DB,
		) (workerHostRuntime, error) {
			return runtime, nil
		},
	}
}

func configuredWorkerHostState(publicURL string) config.State {
	return config.State{
		Status: config.StatusConfigured,
		Config: config.WithDefaults(config.Config{
			HTTP: config.HTTP{
				PublicURL:           publicURL,
				ShutdownGracePeriod: time.Second,
			},
			Database: config.Database{
				Driver: "postgres",
				DSN:    "postgres://worker.example.test/application",
			},
			Jobs: config.Jobs{
				Driver:      "postgres",
				WorkerID:    "test-worker",
				Concurrency: 2,
			},
		}),
		Installation: &config.Installation{},
	}
}

type workerHostRoundTripFunc func(*http.Request) (*http.Response, error)

func (function workerHostRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func workerHostTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
