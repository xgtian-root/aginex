package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type testHTTPRuntime struct {
	shutdown func(context.Context) error
	close    func() error
}

func (runtime testHTTPRuntime) Shutdown(ctx context.Context) error {
	return runtime.shutdown(ctx)
}

func (runtime testHTTPRuntime) Close() error {
	return runtime.close()
}

type testLifecycleRuntime struct {
	shutdown func(context.Context) error
}

func (runtime testLifecycleRuntime) Shutdown(ctx context.Context) error {
	return runtime.shutdown(ctx)
}

func TestShutdownRuntimeForceClosesBeforeFreshLifecycleBudget(t *testing.T) {
	var forceClosed atomic.Bool
	var lifecycleCalls atomic.Int32
	httpServer := testHTTPRuntime{
		shutdown: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			forceClosed.Store(true)
			return nil
		},
	}
	lifecycle := testLifecycleRuntime{
		shutdown: func(ctx context.Context) error {
			lifecycleCalls.Add(1)
			if !forceClosed.Load() {
				t.Fatal("lifecycle stopped before active HTTP requests were closed")
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf(
					"lifecycle received expired HTTP drain context: %v",
					err,
				)
			}
			return nil
		},
	}

	err := shutdownRuntime(
		httpServer,
		lifecycle,
		5*time.Millisecond,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if got := lifecycleCalls.Load(); got != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", got)
	}
}

func TestShutdownRuntimeDoesNotForceCloseAfterCleanDrain(t *testing.T) {
	var closeCalls atomic.Int32
	var lifecycleCalls atomic.Int32
	err := shutdownRuntime(
		testHTTPRuntime{
			shutdown: func(context.Context) error { return nil },
			close: func() error {
				closeCalls.Add(1)
				return nil
			},
		},
		testLifecycleRuntime{
			shutdown: func(ctx context.Context) error {
				lifecycleCalls.Add(1)
				return ctx.Err()
			},
		},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := closeCalls.Load(); got != 0 {
		t.Fatalf("force close calls = %d, want 0", got)
	}
	if got := lifecycleCalls.Load(); got != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", got)
	}
}
