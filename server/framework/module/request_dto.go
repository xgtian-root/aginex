package module

import "context"

type validatedRequestDTOContextKey struct{}

// ContextWithValidatedRequestDTO stores a request value that was decoded and
// validated from the operation's registered APIContract.
func ContextWithValidatedRequestDTO(
	ctx context.Context,
	value any,
) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, validatedRequestDTOContextKey{}, value)
}

// ValidatedRequestDTOFromContext returns the typed value decoded from the
// registered API contract. Module handlers should consume this value instead
// of independently decoding the request body into a different type.
func ValidatedRequestDTOFromContext[T any](ctx context.Context) (T, bool) {
	var zero T
	if ctx == nil {
		return zero, false
	}
	value, ok := ctx.Value(validatedRequestDTOContextKey{}).(T)
	if !ok {
		return zero, false
	}
	return value, true
}
