package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/module"
)

func TestApplicationLifecycleStartBoundsHookIgnoringContext(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	t.Cleanup(unblock)

	registry := module.NewRegistry()
	if err := registry.RegisterLifecycleHook(module.LifecycleHook{
		Name: "stuck-start",
		Start: func(context.Context) error {
			close(entered)
			<-release
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	instance := &App{registry: registry}
	startContext, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- instance.Start(startContext)
	}()
	<-entered

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf(
				"start error = %v, want context deadline exceeded",
				err,
			)
		}
		if !strings.Contains(err.Error(), `start lifecycle hook "stuck-start"`) {
			t.Fatalf("start error = %q, want hook identity", err)
		}
	case <-time.After(250 * time.Millisecond):
		unblock()
		<-result
		t.Fatal("Start remained blocked after its context deadline")
	}
}

func TestApplicationLifecycleShutdownBoundsReverseStopIgnoringContext(
	t *testing.T,
) {
	var events []string
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	t.Cleanup(unblock)

	registry := module.NewRegistry()
	if err := registry.RegisterLifecycleHook(module.LifecycleHook{
		Name:  "alpha",
		Start: func(context.Context) error { return nil },
		Stop: func(context.Context) error {
			events = append(events, "alpha.stop")
			close(entered)
			<-release
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterLifecycleHook(module.LifecycleHook{
		Name:  "beta",
		Start: func(context.Context) error { return nil },
		Stop: func(context.Context) error {
			events = append(events, "beta.stop")
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	instance := &App{
		registry: registry,
		lifecycle: appLifecycle{
			cleanupTimeout: 20 * time.Millisecond,
		},
	}
	if err := instance.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- instance.Shutdown(t.Context())
	}()
	<-entered

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf(
				"shutdown error = %v, want context deadline exceeded",
				err,
			)
		}
		if !strings.Contains(err.Error(), `stop lifecycle hook "alpha"`) {
			t.Fatalf("shutdown error = %q, want hook identity", err)
		}
	case <-time.After(250 * time.Millisecond):
		unblock()
		<-result
		t.Fatal("Shutdown remained blocked after its cleanup deadline")
	}

	if want := []string{"beta.stop", "alpha.stop"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestApplicationLifecyclePreservesRegistryOrder(t *testing.T) {
	var events []string
	registry := module.NewRegistry()
	for _, name := range []string{"alpha", "beta"} {
		name := name
		if err := registry.RegisterLifecycleHook(module.LifecycleHook{
			Name: name,
			Start: func(context.Context) error {
				events = append(events, name+".start")
				return nil
			},
			Stop: func(context.Context) error {
				events = append(events, name+".stop")
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	instance := &App{registry: registry}
	if err := instance.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := instance.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"alpha.start",
		"beta.start",
		"beta.stop",
		"alpha.stop",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}
