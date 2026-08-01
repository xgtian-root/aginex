package database

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/xgtian-root/aginex/framework/observability"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultSlowQueryThreshold = 500 * time.Millisecond

// safeLogger deliberately omits SQL, bound parameters, and database error
// strings. Those values can contain credentials or user content. Operation
// outcomes and durations remain available through the GORM observability
// callbacks; this logger provides structured diagnostics for failures and slow
// calls without creating a second secret-bearing channel.
type safeLogger struct {
	log           *slog.Logger
	level         logger.LogLevel
	slowThreshold time.Duration
}

func newSafeLogger(log *slog.Logger) logger.Interface {
	if log == nil {
		log = slog.Default()
	}
	return &safeLogger{
		log:           log,
		level:         logger.Warn,
		slowThreshold: defaultSlowQueryThreshold,
	}
}

func (current *safeLogger) LogMode(level logger.LogLevel) logger.Interface {
	cloned := *current
	cloned.level = level
	return &cloned
}

func (current *safeLogger) Info(
	ctx context.Context,
	_ string,
	_ ...interface{},
) {
	if current.level < logger.Info {
		return
	}
	current.log.InfoContext(ctx, "Database diagnostic", traceAttributes(ctx)...)
}

func (current *safeLogger) Warn(
	ctx context.Context,
	_ string,
	_ ...interface{},
) {
	if current.level < logger.Warn {
		return
	}
	current.log.WarnContext(ctx, "Database warning", traceAttributes(ctx)...)
}

func (current *safeLogger) Error(
	ctx context.Context,
	_ string,
	_ ...interface{},
) {
	if current.level < logger.Error {
		return
	}
	current.log.ErrorContext(ctx, "Database error", traceAttributes(ctx)...)
}

func (current *safeLogger) Trace(
	ctx context.Context,
	started time.Time,
	statement func() (string, int64),
	err error,
) {
	if current.level == logger.Silent {
		return
	}
	duration := time.Since(started)
	isFailure := err != nil && !errors.Is(err, gorm.ErrRecordNotFound)
	isSlow := duration > current.slowThreshold
	if !isFailure && (!isSlow || current.level < logger.Warn) {
		return
	}

	rows := int64(-1)
	if statement != nil {
		// GORM exposes rows only through a callback that also renders SQL.
		// Discard the statement immediately and never attach it to a log.
		_, rows = statement()
	}
	attributes := append(
		traceAttributes(ctx),
		"duration", duration,
		"rows", rows,
	)
	if isFailure && current.level >= logger.Error {
		current.log.ErrorContext(
			ctx,
			"Database operation failed",
			attributes...,
		)
		return
	}
	if isSlow {
		current.log.WarnContext(
			ctx,
			"Slow database operation",
			attributes...,
		)
	}
}

func traceAttributes(ctx context.Context) []any {
	spanContext, ok := observability.SpanContextFromContext(ctx)
	if !ok {
		return nil
	}
	return []any{
		"trace_id", spanContext.TraceID(),
		"span_id", spanContext.SpanID(),
	}
}
