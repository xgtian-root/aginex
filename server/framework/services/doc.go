// Package services exposes the runtime dependencies available to compiled-in
// Aginex module handlers, lifecycle hooks, readiness checks, and jobs.
//
// Registration remains side-effect-free: modules resolve these dependencies
// from the execution context only after an API or worker runtime starts.
package services
