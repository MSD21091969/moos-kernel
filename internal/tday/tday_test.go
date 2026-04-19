package tday

import (
	"testing"
	"time"
)

// TestT0_MatchesDoctrinaryEpoch — the epoch is 2025-11-01 00:00 CEST
// which is 2025-10-31 23:00 UTC. Locking this in so future callers can
// rely on the exact moment.
func TestT0_MatchesDoctrinaryEpoch(t *testing.T) {
	if T0.UTC() != time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC) {
		t.Errorf("T0 drifted from documented epoch: got %v", T0.UTC())
	}
}

// TestAt_DayZero — T0 itself is day 0.
func TestAt_DayZero(t *testing.T) {
	if got := At(T0); got != 0 {
		t.Errorf("At(T0) = %d; want 0", got)
	}
}

// TestAt_OneDay — exactly 24h after T0 is day 1.
func TestAt_OneDay(t *testing.T) {
	if got := At(T0.Add(24 * time.Hour)); got != 1 {
		t.Errorf("At(T0+24h) = %d; want 1", got)
	}
}

// TestAt_Just_Before_Boundary — a nanosecond before midnight UTC on day
// N still returns N (integer truncation, not float rounding).
func TestAt_Just_Before_Boundary(t *testing.T) {
	justBefore := T0.Add(24*time.Hour - time.Nanosecond)
	if got := At(justBefore); got != 0 {
		t.Errorf("At(T0+24h-1ns) = %d; want 0", got)
	}
}

// TestAt_Just_After_Boundary — the nanosecond at exactly the boundary
// flips to the next day.
func TestAt_Just_After_Boundary(t *testing.T) {
	justAfter := T0.Add(24 * time.Hour)
	if got := At(justAfter); got != 1 {
		t.Errorf("At(T0+24h) = %d; want 1", got)
	}
}

// TestAt_T169_Reference — sanity: 2026-04-19 00:00 UTC is T=169.
// Matches sam's manual wall-clock reference of "T=169 0908 CEST".
// From T0 (2025-10-31 23:00 UTC) to 2026-04-19 00:00 UTC is 169 days + 1h;
// integer division drops the hour → 169.
func TestAt_T169_Reference(t *testing.T) {
	apr19 := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	if got := At(apr19); got != 169 {
		t.Errorf("At(2026-04-19 00:00 UTC) = %d; want 169", got)
	}
}

// TestNow_NotNegative — a smoke test that Now() is non-negative for any
// wall clock after T0.
func TestNow_NotNegative(t *testing.T) {
	if got := Now(); got < 0 {
		t.Errorf("Now() = %d; must be non-negative after T0", got)
	}
}

// TestAt_BeforeEpoch — calls before T0 return negative values (integer
// division rounds toward zero in Go, so -1ns → -1 full-day is actually
// 0 because truncation makes it not yet hit a full -1 day). Document
// the behaviour so callers don't rely on negative values meaning
// "before the project started".
func TestAt_BeforeEpoch(t *testing.T) {
	beforeFullDay := T0.Add(-12 * time.Hour)
	// -12h / 24h = 0 in integer division (truncation toward zero).
	if got := At(beforeFullDay); got != 0 {
		t.Errorf("At(T0-12h) = %d; want 0 (integer truncation toward zero)", got)
	}
	beforeOneDay := T0.Add(-25 * time.Hour)
	if got := At(beforeOneDay); got != -1 {
		t.Errorf("At(T0-25h) = %d; want -1 (past a full day before epoch)", got)
	}
}
