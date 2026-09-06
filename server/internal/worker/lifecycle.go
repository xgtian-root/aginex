package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xgtian-root/aginex/server/framework/module"
)

var (
	ErrLifecycleStopped = errors.New(
		"worker lifecycle is already stopped",
	)
	ErrLifecycleTransition = errors.New(
		"worker lifecycle transition is already in progress",
	)
)

const defaultLifecycleCleanupTimeout = 30 * time.Second

type workerLifecycleState uint8

const (
	workerLifecycleIdle workerLifecycleState = iota
	workerLifecycleStarting
	workerLifecycleStarted
	workerLifecycleStopping
	workerLifecycleStopped
	workerLifecycleFailed
)

type workerLifecycle struct {
	mu             sync.Mutex
	state          workerLifecycleState
	started        []module.LifecycleHook
	startErr       error
	shutdownErr    error
	cleanupTimeout time.Duration
}

// Start initializes registered application-module hooks before any job
// handler is dispatched. Repeated calls after success are harmless.
func (runtime *Runtime) Start(ctx context.Context) error {
	if runtime == nil || runtime.registry == nil {
		return errors.New("worker runtime is required")
	}
	if ctx == nil {
		return errors.New("worker start context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	runtime.lifecycle.mu.Lock()
	switch runtime.lifecycle.state {
	case workerLifecycleStarted:
		runtime.lifecycle.mu.Unlock()
		return nil
	case workerLifecycleStopped:
		runtime.lifecycle.mu.Unlock()
		return ErrLifecycleStopped
	case workerLifecycleFailed:
		err := runtime.lifecycle.startErr
		runtime.lifecycle.mu.Unlock()
		return err
	case workerLifecycleStarting, workerLifecycleStopping:
		runtime.lifecycle.mu.Unlock()
		return ErrLifecycleTransition
	case workerLifecycleIdle:
		runtime.lifecycle.state = workerLifecycleStarting
	}
	runtime.lifecycle.mu.Unlock()

	hooks := runtime.registry.LifecycleHooks()
	started := make([]module.LifecycleHook, 0, len(hooks))
	for _, hook := range hooks {
		if hook.Start != nil {
			err := callLifecycleHook(
				runtime.contextWithRuntimeServices(ctx),
				hook.Start,
			)
			if err != nil {
				cleanupContext, cancel := runtime.lifecycle.cleanupContext(ctx)
				rollbackErr := stopWorkerLifecycleHooks(
					runtime.contextWithRuntimeServices(cleanupContext),
					started,
				)
				cancel()
				result := errors.Join(
					fmt.Errorf(
						"start worker lifecycle hook %q: %w",
						hook.Name,
						err,
					),
					rollbackErr,
				)
				runtime.lifecycle.mu.Lock()
				runtime.lifecycle.state = workerLifecycleFailed
				runtime.lifecycle.started = nil
				runtime.lifecycle.startErr = result
				runtime.lifecycle.mu.Unlock()
				return result
			}
		}
		started = append(started, hook)
	}

	runtime.lifecycle.mu.Lock()
	runtime.lifecycle.started = started
	runtime.lifecycle.state = workerLifecycleStarted
	runtime.lifecycle.mu.Unlock()
	return nil
}

// Shutdown stops successfully started hooks in reverse order using an
// independent bounded cleanup context.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil {
		return errors.New("worker runtime is required")
	}
	if ctx == nil {
		return errors.New("worker shutdown context is required")
	}

	runtime.lifecycle.mu.Lock()
	switch runtime.lifecycle.state {
	case workerLifecycleIdle:
		runtime.lifecycle.state = workerLifecycleStopped
		runtime.lifecycle.mu.Unlock()
		return nil
	case workerLifecycleStopped:
		err := runtime.lifecycle.shutdownErr
		runtime.lifecycle.mu.Unlock()
		return err
	case workerLifecycleFailed:
		err := runtime.lifecycle.startErr
		runtime.lifecycle.mu.Unlock()
		return err
	case workerLifecycleStarting, workerLifecycleStopping:
		runtime.lifecycle.mu.Unlock()
		return ErrLifecycleTransition
	case workerLifecycleStarted:
		runtime.lifecycle.state = workerLifecycleStopping
	}
	started := append(
		[]module.LifecycleHook(nil),
		runtime.lifecycle.started...,
	)
	runtime.lifecycle.mu.Unlock()

	cleanupContext, cancel := runtime.lifecycle.cleanupContext(ctx)
	err := stopWorkerLifecycleHooks(
		runtime.contextWithRuntimeServices(cleanupContext),
		started,
	)
	cancel()

	runtime.lifecycle.mu.Lock()
	runtime.lifecycle.state = workerLifecycleStopped
	runtime.lifecycle.started = nil
	runtime.lifecycle.shutdownErr = err
	runtime.lifecycle.mu.Unlock()
	return err
}

func (lifecycle *workerLifecycle) cleanupContext(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	timeout := lifecycle.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultLifecycleCleanupTimeout
	}
	return context.WithTimeout(
		context.WithoutCancel(parent),
		timeout,
	)
}

func stopWorkerLifecycleHooks(
	ctx context.Context,
	hooks []module.LifecycleHook,
) error {
	var result error
	for index := len(hooks) - 1; index >= 0; index-- {
		hook := hooks[index]
		if hook.Stop == nil {
			continue
		}
		if err := callLifecycleHook(ctx, hook.Stop); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf(
					"stop worker lifecycle hook %q: %w",
					hook.Name,
					err,
				),
			)
		}
	}
	return result
}

func callLifecycleHook(
	ctx context.Context,
	hook func(context.Context) error,
) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf(
					"lifecycle hook panic: %v",
					recovered,
				)
			}
		}()
		done <- hook(ctx)
	}()
	select {
	case result = <-done:
		return result
	case <-ctx.Done():
		return ctx.Err()
	}
}
