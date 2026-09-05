package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xgtian-root/aginex/backend/framework/module"
)

var (
	ErrLifecycleStopped = errors.New(
		"application lifecycle is already stopped",
	)
	ErrLifecycleTransition = errors.New(
		"application lifecycle transition is already in progress",
	)
)

const (
	defaultLifecycleCleanupTimeout = 30 * time.Second
	lifecycleHookResultGracePeriod = 5 * time.Millisecond
)

type lifecycleState uint8

const (
	lifecycleIdle lifecycleState = iota
	lifecycleStarting
	lifecycleStarted
	lifecycleStopping
	lifecycleStopped
	lifecycleFailed
)

type appLifecycle struct {
	mu             sync.Mutex
	state          lifecycleState
	started        []module.LifecycleHook
	startErr       error
	shutdownErr    error
	cleanupTimeout time.Duration
}

// Start invokes registered lifecycle hooks in deterministic registry order.
// Callbacks always run outside the state lock. If a hook fails, already-started
// hooks are stopped in reverse order using an independent bounded cleanup
// context, even when the caller's start context has expired.
func (a *App) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application start context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	a.lifecycle.mu.Lock()
	switch a.lifecycle.state {
	case lifecycleStarted:
		a.lifecycle.mu.Unlock()
		return nil
	case lifecycleStopped:
		a.lifecycle.mu.Unlock()
		return ErrLifecycleStopped
	case lifecycleFailed:
		err := a.lifecycle.startErr
		a.lifecycle.mu.Unlock()
		return err
	case lifecycleStarting, lifecycleStopping:
		a.lifecycle.mu.Unlock()
		return ErrLifecycleTransition
	case lifecycleIdle:
		a.lifecycle.state = lifecycleStarting
	}
	a.lifecycle.mu.Unlock()

	hooks := a.registry.LifecycleHooks()
	started := make([]module.LifecycleHook, 0, len(hooks))
	for _, hook := range hooks {
		if hook.Start != nil {
			if err := callAppLifecycleHook(
				a.contextWithRuntimeServices(ctx),
				hook.Start,
			); err != nil {
				cleanupContext, cancel := a.lifecycle.cleanupContext(ctx)
				rollbackErr := stopLifecycleHooks(
					a.contextWithRuntimeServices(cleanupContext),
					started,
				)
				cancel()
				result := errors.Join(
					fmt.Errorf(
						"start lifecycle hook %q: %w",
						hook.Name,
						err,
					),
					rollbackErr,
				)
				a.lifecycle.mu.Lock()
				a.lifecycle.state = lifecycleFailed
				a.lifecycle.started = nil
				a.lifecycle.startErr = result
				a.lifecycle.mu.Unlock()
				return result
			}
		}
		started = append(started, hook)
	}

	a.lifecycle.mu.Lock()
	a.lifecycle.started = started
	a.lifecycle.state = lifecycleStarted
	a.lifecycle.mu.Unlock()
	return nil
}

// Shutdown invokes successfully started lifecycle hooks in reverse order.
// Repeated calls return the original shutdown result instead of hiding a
// cleanup failure.
func (a *App) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application shutdown context is required")
	}

	a.lifecycle.mu.Lock()
	switch a.lifecycle.state {
	case lifecycleIdle:
		a.lifecycle.state = lifecycleStopped
		a.lifecycle.mu.Unlock()
		return nil
	case lifecycleStopped:
		err := a.lifecycle.shutdownErr
		a.lifecycle.mu.Unlock()
		return err
	case lifecycleFailed:
		err := a.lifecycle.startErr
		a.lifecycle.mu.Unlock()
		return err
	case lifecycleStarting, lifecycleStopping:
		a.lifecycle.mu.Unlock()
		return ErrLifecycleTransition
	case lifecycleStarted:
		a.lifecycle.state = lifecycleStopping
	}
	started := append(
		[]module.LifecycleHook(nil),
		a.lifecycle.started...,
	)
	a.lifecycle.mu.Unlock()

	cleanupContext, cancel := a.lifecycle.cleanupContext(ctx)
	err := stopLifecycleHooks(
		a.contextWithRuntimeServices(cleanupContext),
		started,
	)
	cancel()

	a.lifecycle.mu.Lock()
	a.lifecycle.state = lifecycleStopped
	a.lifecycle.started = nil
	a.lifecycle.shutdownErr = err
	a.lifecycle.mu.Unlock()
	return err
}

func (lifecycle *appLifecycle) cleanupContext(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	timeout := lifecycle.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultLifecycleCleanupTimeout
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func stopLifecycleHooks(
	ctx context.Context,
	hooks []module.LifecycleHook,
) error {
	var result error
	for index := len(hooks) - 1; index >= 0; index-- {
		hook := hooks[index]
		if hook.Stop == nil {
			continue
		}
		if err := callAppLifecycleHook(ctx, hook.Stop); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf("stop lifecycle hook %q: %w", hook.Name, err),
			)
		}
	}
	return result
}

// callAppLifecycleHook bounds the application's wait for a hook by ctx even
// when an extension fails to observe cancellation. The buffered result channel
// lets a late-returning hook finish without blocking after the caller leaves.
func callAppLifecycleHook(
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
					"application lifecycle hook panic: %v",
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
		// A hook may cancel the supplied context immediately before returning
		// its own more specific error. Allow that result one scheduler handoff
		// so cancellation does not erase the original failure, while keeping
		// an uncooperative hook strictly bounded.
		timer := time.NewTimer(lifecycleHookResultGracePeriod)
		defer timer.Stop()
		select {
		case result = <-done:
			return result
		case <-timer.C:
			return ctx.Err()
		}
	}
}
