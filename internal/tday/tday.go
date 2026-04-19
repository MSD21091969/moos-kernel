// Package tday is the single source of truth for the mo:os-time epoch
// and the wall-clock-to-T-day conversion.
//
// Prior to T=169 the epoch and the conversion were duplicated across
// cmd/moos/main.go, internal/transport/server.go, and internal/kernel/sweep.go.
// A drift between them would have shown up as the sweep firing a hook one
// day before the t-cone projected it open (or vice versa). Consolidating
// here eliminates the drift risk and lets future calendar adjustments
// land in one place.
//
// The conversion uses integer duration division (time.Since / 24h)
// rather than Duration.Hours()/24 to avoid float64 rounding errors near
// day boundaries (a round-9 review finding — see PR #23).
package tday

import "time"

// T0 is mo:os-time day zero: 2025-11-01 00:00 CEST = 2025-10-31 23:00 UTC.
//
// Kept as a package-level var (not const — time.Time isn't a Go constant)
// so tests in this package can temporarily override and restore it if
// they ever need deterministic wall-clock semantics. Regular callers
// should treat it as immutable.
var T0 = time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC)

// Now returns the current calendar T-day, derived from wall clock.
//
// Integer duration division: a tick that lands at 23:59:59.999999999 of
// day N still returns N, and the next nanosecond flips atomically to N+1.
// This is safer than Hours()/24 float arithmetic near boundaries.
func Now() int {
	return int(time.Since(T0) / (24 * time.Hour))
}

// At returns the T-day for a specific moment. Useful for testing and
// for tagging governance_proposal.created_at with an auditable T-day.
func At(t time.Time) int {
	return int(t.Sub(T0) / (24 * time.Hour))
}
