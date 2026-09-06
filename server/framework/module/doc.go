// Package module defines the public, side-effect-free contract used to compose
// Aginex applications from compiled-in modules.
//
// A Registry validates and snapshots both declarative contracts and executable
// adapters. It never performs side effects during registration. API, worker,
// migrate, and lifecycle entrypoints consume the same immutable module set and
// execute the registered routes, policies, migrations, jobs, and hooks at
// their explicit runtime boundaries.
package module
