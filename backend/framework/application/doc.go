// Package application exposes the single composition root used by applications
// built on Aginex.
//
// A Definition owns one immutable module set and supplies it consistently to
// API, worker, migration, bootstrap, and OpenAPI entry points. This prevents a
// derived application from silently omitting a module in one process. The
// empty definition contains only fixed framework capabilities; business
// resources such as the official files module and starter products/dashboard
// must be registered explicitly.
package application
