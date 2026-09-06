package observability

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

const TraceParentHeader = "traceparent"

type spanContextKey struct{}

type storedSpanContext struct {
	context SpanContext
	valid   bool
}

// SpanContext is an immutable W3C trace and span identity.
type SpanContext struct {
	traceID    [16]byte
	spanID     [8]byte
	traceFlags byte
	remote     bool
	valid      bool
}

// IsValid reports whether both identifiers satisfy the W3C non-zero rules.
func (s SpanContext) IsValid() bool {
	return s.valid && !allZero(s.traceID[:]) && !allZero(s.spanID[:])
}

// TraceID returns the lowercase, 32-character trace identifier.
func (s SpanContext) TraceID() string {
	if !s.IsValid() {
		return ""
	}
	return hex.EncodeToString(s.traceID[:])
}

// SpanID returns the lowercase, 16-character span identifier.
func (s SpanContext) SpanID() string {
	if !s.IsValid() {
		return ""
	}
	return hex.EncodeToString(s.spanID[:])
}

// TraceFlags returns the W3C trace-flags byte.
func (s SpanContext) TraceFlags() byte {
	return s.traceFlags
}

// IsRemote reports whether this context was extracted from an inbound
// traceparent rather than created by this process.
func (s SpanContext) IsRemote() bool {
	return s.IsValid() && s.remote
}

// TraceParent renders a version 00 W3C traceparent value.
func (s SpanContext) TraceParent() string {
	if !s.IsValid() {
		return ""
	}
	return fmt.Sprintf(
		"00-%s-%s-%02x",
		s.TraceID(),
		s.SpanID(),
		s.traceFlags,
	)
}

// Extract returns a context containing a valid remote W3C parent. Invalid or
// unsupported headers deliberately clear any inherited parent so the next
// Start creates a fresh trace.
func (r *Recorder) Extract(ctx context.Context, traceParent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, ok := parseTraceParent(traceParent)
	return context.WithValue(
		ctx,
		spanContextKey{},
		storedSpanContext{context: parsed, valid: ok},
	)
}

// SpanContextFromContext returns the current local span or extracted remote
// parent.
func SpanContextFromContext(ctx context.Context) (SpanContext, bool) {
	if ctx == nil {
		return SpanContext{}, false
	}
	stored, ok := ctx.Value(spanContextKey{}).(storedSpanContext)
	if !ok || !stored.valid || !stored.context.IsValid() {
		return SpanContext{}, false
	}
	return stored.context, true
}

// TraceParentFromContext renders the current context for downstream
// propagation. It returns an empty string until a valid parent is extracted or
// Recorder.Start creates a span.
func TraceParentFromContext(ctx context.Context) string {
	spanContext, ok := SpanContextFromContext(ctx)
	if !ok {
		return ""
	}
	return spanContext.TraceParent()
}

func contextWithSpanContext(
	ctx context.Context,
	spanContext SpanContext,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(
		ctx,
		spanContextKey{},
		storedSpanContext{context: spanContext, valid: spanContext.IsValid()},
	)
}

func parseTraceParent(candidate string) (SpanContext, bool) {
	value := strings.ToLower(strings.TrimSpace(candidate))
	if len(value) != 55 ||
		value[:2] != "00" ||
		value[2] != '-' ||
		value[35] != '-' ||
		value[52] != '-' {
		return SpanContext{}, false
	}
	traceID, err := hex.DecodeString(value[3:35])
	if err != nil || allZero(traceID) {
		return SpanContext{}, false
	}
	spanID, err := hex.DecodeString(value[36:52])
	if err != nil || allZero(spanID) {
		return SpanContext{}, false
	}
	flags, err := hex.DecodeString(value[53:55])
	if err != nil {
		return SpanContext{}, false
	}
	parsed := SpanContext{traceFlags: flags[0], remote: true, valid: true}
	copy(parsed.traceID[:], traceID)
	copy(parsed.spanID[:], spanID)
	return parsed, true
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
