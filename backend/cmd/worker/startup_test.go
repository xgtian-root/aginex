package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/config"
)

func TestWaitForAPIInitializationRequiresMarkerModeAndReadiness(
	t *testing.T,
) {
	t.Parallel()

	var modeCalls atomic.Int32
	var readyCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", request.Method)
		}
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		switch request.URL.Path {
		case "/api/v1/system/mode":
			call := modeCalls.Add(1)
			if call == 1 {
				return workerTestResponse(
					http.StatusOK,
					`{"mode":"setup"}`,
				), nil
			}
			return workerTestResponse(
				http.StatusOK,
				`{"mode":"application"}`,
			), nil
		case "/health/ready":
			call := readyCalls.Add(1)
			if call == 1 {
				return workerTestResponse(
					http.StatusServiceUnavailable,
					"",
				), nil
			}
			return workerTestResponse(http.StatusOK, ""), nil
		default:
			t.Errorf("unexpected worker startup request %s", request.URL.Path)
			return workerTestResponse(http.StatusNotFound, ""), nil
		}
	})}

	configured := configuredWorkerState("http://api.example.test")
	var loadCalls atomic.Int32
	loader := func() (config.State, error) {
		switch loadCalls.Add(1) {
		case 1:
			return config.State{Status: config.StatusSetup}, nil
		case 2:
			waitingForMarker := configured
			waitingForMarker.NeedsEnvironmentMarker = true
			return waitingForMarker, nil
		default:
			return configured, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := waitForAPIInitialization(
		ctx,
		loader,
		client,
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.HTTP.PublicURL != "http://api.example.test" {
		t.Fatalf("public URL = %q, want configured URL", got.HTTP.PublicURL)
	}
	if got := loadCalls.Load(); got != 5 {
		t.Fatalf("installation state loads = %d, want 5", got)
	}
	if got := modeCalls.Load(); got != 3 {
		t.Fatalf("mode probes = %d, want 3", got)
	}
	if got := readyCalls.Load(); got != 2 {
		t.Fatalf("readiness probes = %d, want 2", got)
	}
}

func TestWaitForAPIInitializationFailsImmediatelyOnInvalidState(
	t *testing.T,
) {
	t.Parallel()

	sentinel := errors.New("installation file permissions are unsafe")
	var calls atomic.Int32
	_, err := waitForAPIInitialization(
		context.Background(),
		func() (config.State, error) {
			calls.Add(1)
			return config.State{}, sentinel
		},
		&http.Client{},
		time.Hour,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want installation error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("installation state loads = %d, want 1", got)
	}
}

func TestWaitForAPIInitializationRejectsConfiguredStateWithoutMarker(
	t *testing.T,
) {
	t.Parallel()

	_, err := waitForAPIInitialization(
		context.Background(),
		func() (config.State, error) {
			return config.State{
				Status: config.StatusConfigured,
				Config: config.Config{HTTP: config.HTTP{
					PublicURL: "http://api.example.test",
				}},
			}, nil
		},
		&http.Client{},
		time.Hour,
	)
	if !errors.Is(err, errInvalidWorkerStartupConfiguration) {
		t.Fatalf("error = %v, want invalid configuration", err)
	}
}

func TestWaitForAPIInitializationIgnoresProbeErrorsUntilCanceled(
	t *testing.T,
) {
	t.Parallel()

	const transportSecret = "transport-secret-must-not-escape"
	var probes atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		probes.Add(1)
		return nil, errors.New(transportSecret)
	})}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := waitForAPIInitialization(
		ctx,
		func() (config.State, error) {
			return configuredWorkerState("http://api.example.test"), nil
		},
		client,
		time.Millisecond,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if strings.Contains(err.Error(), transportSecret) {
		t.Fatalf("worker startup error leaked transport details: %v", err)
	}
	if probes.Load() == 0 {
		t.Fatal("worker did not probe the API")
	}
}

func TestWaitForAPIInitializationReloadsAndRejectsCorruption(
	t *testing.T,
) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		return workerTestResponse(http.StatusOK, `{"mode":"setup"}`), nil
	})}

	sentinel := errors.New("installation configuration became invalid")
	var loads atomic.Int32
	_, err := waitForAPIInitialization(
		context.Background(),
		func() (config.State, error) {
			if loads.Add(1) == 1 {
				return configuredWorkerState("http://api.example.test"), nil
			}
			return config.State{}, sentinel
		},
		client,
		time.Millisecond,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want reloaded installation error", err)
	}
}

func TestApplicationReadyBoundsResponsesAndRequiresExactMode(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name          string
		modeBody      string
		readyStatus   int
		wantReady     bool
		wantReadyCall bool
	}{
		{
			name:     "setup mode",
			modeBody: `{"mode":"setup"}`,
		},
		{
			name:     "unknown mode field",
			modeBody: `{"mode":"application","detail":"ignored"}`,
		},
		{
			name:     "multiple JSON values",
			modeBody: `{"mode":"application"} {}`,
		},
		{
			name:          "ready application",
			modeBody:      `{"mode":"application"}`,
			readyStatus:   http.StatusOK,
			wantReady:     true,
			wantReadyCall: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var readyCalls atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(
				request *http.Request,
			) (*http.Response, error) {
				switch request.URL.Path {
				case "/api/v1/system/mode":
					return workerTestResponse(
						http.StatusOK,
						test.modeBody,
					), nil
				case "/health/ready":
					readyCalls.Add(1)
					return workerTestResponse(
						test.readyStatus,
						"",
					), nil
				default:
					return workerTestResponse(
						http.StatusNotFound,
						"",
					), nil
				}
			})}

			got, err := applicationReady(
				context.Background(),
				client,
				"http://api.example.test",
			)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.wantReady {
				t.Fatalf("ready = %v, want %v", got, test.wantReady)
			}
			if got := readyCalls.Load() > 0; got != test.wantReadyCall {
				t.Fatalf(
					"readiness endpoint called = %v, want %v",
					got,
					test.wantReadyCall,
				)
			}
		})
	}

	largeBody := &countingReadCloser{Reader: strings.NewReader(
		`{"mode":"application"}` +
			strings.Repeat(" ", workerStartupResponseLimit),
	)}
	client := &http.Client{Transport: roundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       largeBody,
			Header:     make(http.Header),
		}, nil
	})}
	ready, err := applicationReady(
		context.Background(),
		client,
		"http://api.example.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("oversized mode response unexpectedly released worker")
	}
	if got := largeBody.bytesRead.Load(); got != workerStartupResponseLimit+1 {
		t.Fatalf(
			"mode response bytes read = %d, want %d",
			got,
			workerStartupResponseLimit+1,
		)
	}
}

func TestApplicationReadyRejectsCredentialedPublicURLWithoutLeakingIt(
	t *testing.T,
) {
	t.Parallel()

	const secret = "worker-url-secret"
	_, err := applicationReady(
		context.Background(),
		&http.Client{},
		"https://worker:"+secret+"@api.example.test",
	)
	if !errors.Is(err, errInvalidWorkerStartupConfiguration) {
		t.Fatalf("error = %v, want invalid configuration", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid URL error leaked credentials: %v", err)
	}
}

func configuredWorkerState(publicURL string) config.State {
	installation, err := config.NewManagedInstallation(
		config.Database{Driver: "sqlite", DSN: "worker-startup.db"},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		panic(err)
	}
	return config.State{
		Status:       config.StatusConfigured,
		Config:       config.Config{HTTP: config.HTTP{PublicURL: publicURL}},
		Installation: &installation,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func workerTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

type countingReadCloser struct {
	io.Reader
	bytesRead atomic.Int64
}

func (reader *countingReadCloser) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	reader.bytesRead.Add(int64(count))
	return count, err
}

func (*countingReadCloser) Close() error {
	return nil
}
