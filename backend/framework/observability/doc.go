// Package observability provides vendor-neutral tracing and metric contracts.
//
// Recorder always creates and propagates W3C trace context, including when no
// Sink is configured. A deployment can adapt Sink to OpenTelemetry, Prometheus,
// or another backend and is responsible for export buffering and sampling.
//
// Sink methods may be called concurrently. Implementations must therefore be
// concurrency-safe and should return quickly. Records contain immutable value
// objects: attribute accessors return copies rather than the recorder's maps.
// Recorder isolates Sink panics so telemetry cannot fail a business request;
// deployments must monitor their adapter separately for dropped exports.
package observability
