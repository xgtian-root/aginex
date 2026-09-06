package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xgtian-root/aginex/server/framework/observability"
)

var errObservedRateLimitOperation = errors.New("rate limit operation failed")

// Observe decorates a limiter with provider-neutral spans and metrics. The
// policy namespace is the only request-derived attribute; raw keys, limits,
// users, request IDs, and persisted bucket hashes are never observed.
func Observe(
	limiter Limiter,
	recorder *observability.Recorder,
) (Limiter, error) {
	if nilInterface(limiter) {
		return nil, fmt.Errorf("%w: limiter is required", ErrInvalidOption)
	}
	if recorder == nil {
		recorder, _ = observability.NewRecorder(nil)
	}
	return &observedLimiter{limiter: limiter, recorder: recorder}, nil
}

type observedLimiter struct {
	limiter  Limiter
	recorder *observability.Recorder
}

func (limiter *observedLimiter) Consume(
	ctx context.Context,
	request Request,
) (Decision, error) {
	if ctx == nil {
		return Decision{}, fmt.Errorf(
			"%w: context is required",
			ErrInvalidRequest,
		)
	}
	attributes := rateLimitAttributes(request.Namespace)
	operationContext, span := limiter.recorder.Start(
		ctx,
		observability.SpanStart{
			Name:       "ratelimit.consume",
			Kind:       observability.SpanKindInternal,
			Attributes: attributes,
		},
	)
	startedAt := time.Now()
	decision, err := limiter.limiter.Consume(operationContext, request)
	outcome := "allowed"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedRateLimitOperation,
		})
	} else {
		if !decision.Allowed {
			outcome = "denied"
		}
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	recordRateLimitMetric(limiter.recorder, observability.Metric{
		Name:       "ratelimit.consume." + outcome,
		Kind:       observability.MetricCounter,
		Value:      1,
		Unit:       "request",
		Attributes: attributes,
	})
	recordRateLimitMetric(limiter.recorder, observability.Metric{
		Name:       "ratelimit.consume.duration",
		Kind:       observability.MetricHistogram,
		Value:      float64(time.Since(startedAt)) / float64(time.Millisecond),
		Unit:       "ms",
		Attributes: attributes,
	})
	return decision, err
}

func (limiter *observedLimiter) CleanupExpired(
	ctx context.Context,
	batchSize int,
) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf(
			"%w: context is required",
			ErrInvalidRequest,
		)
	}
	operationContext, span := limiter.recorder.Start(
		ctx,
		observability.SpanStart{
			Name: "ratelimit.cleanup",
			Kind: observability.SpanKindInternal,
		},
	)
	startedAt := time.Now()
	removed, err := limiter.limiter.CleanupExpired(operationContext, batchSize)
	outcome := "succeeded"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedRateLimitOperation,
		})
	} else {
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	recordRateLimitMetric(limiter.recorder, observability.Metric{
		Name:  "ratelimit.cleanup." + outcome,
		Kind:  observability.MetricCounter,
		Value: 1,
		Unit:  "request",
	})
	recordRateLimitMetric(limiter.recorder, observability.Metric{
		Name:  "ratelimit.cleanup.duration",
		Kind:  observability.MetricHistogram,
		Value: float64(time.Since(startedAt)) / float64(time.Millisecond),
		Unit:  "ms",
	})
	if err == nil {
		recordRateLimitMetric(limiter.recorder, observability.Metric{
			Name:  "ratelimit.cleanup.removed",
			Kind:  observability.MetricHistogram,
			Value: float64(removed),
			Unit:  "bucket",
		})
	}
	return removed, err
}

func rateLimitAttributes(namespace string) observability.Attributes {
	if !validNamespace(namespace) {
		namespace = "invalid"
	}
	attributes, _ := observability.NewAttributes(map[string]string{
		"namespace": namespace,
	})
	return attributes
}

func recordRateLimitMetric(
	recorder *observability.Recorder,
	metric observability.Metric,
) {
	_ = recorder.RecordMetric(metric)
}
