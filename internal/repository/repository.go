// Package repository contains domain-specific persistence adapters.
//
// The goal of this package is to keep raw SQL out of handler and service
// code: each repository wraps the queries for one aggregate (sessions,
// notifications, scans, ...) behind a typed Go API. Callers exchange
// domain values and typed errors with the repository; only the repository
// itself knows the schema, parameter order, and SQL dialect.
//
// This is the Phase 3 foundation; see remediation_plan.md.
package repository

import "errors"

// ErrNotFound is returned by repository lookups when no row matches.
// Callers should compare with errors.Is so DB-driver wrappings stay
// transparent.
var ErrNotFound = errors.New("repository: not found")
