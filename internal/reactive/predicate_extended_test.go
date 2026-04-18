package reactive

import (
	"testing"

	"moos/kernel/internal/graph"
)

// ============================================================================
// M5 — extended §M14 predicate kinds
// ============================================================================

// baseState returns a minimal state for predicate tests — one program
// with mutable status, referenced by after_urn / on_prop_set tests.
func baseState() *graph.GraphState {
	return &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.p": {
				URN:    "urn:moos:program:sam.p",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
}

// ----------------------------------------------------------------------------
// window — half-open interval [opens_at, closes_at)
// ----------------------------------------------------------------------------

func TestPredicate_Window_InRange(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "window", "opens_at": 100, "closes_at": 200}
	for _, ct := range []int{100, 101, 150, 199} {
		if !EvaluateThookPredicate(pred, state, ct) {
			t.Errorf("window[100,200) should fire at currentT=%d", ct)
		}
	}
}

func TestPredicate_Window_BeforeOpens(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "window", "opens_at": 100, "closes_at": 200}
	for _, ct := range []int{0, 50, 99} {
		if EvaluateThookPredicate(pred, state, ct) {
			t.Errorf("window[100,200) should NOT fire before opens_at at currentT=%d", ct)
		}
	}
}

func TestPredicate_Window_AtOrAfterCloses(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "window", "opens_at": 100, "closes_at": 200}
	for _, ct := range []int{200, 201, 500} {
		if EvaluateThookPredicate(pred, state, ct) {
			t.Errorf("window[100,200) is half-open; must NOT fire at currentT=%d (>= closes_at)", ct)
		}
	}
}

func TestPredicate_Window_EmptyRange(t *testing.T) {
	state := baseState()
	// opens_at == closes_at → empty range → never fires.
	pred := map[string]any{"kind": "window", "opens_at": 100, "closes_at": 100}
	if EvaluateThookPredicate(pred, state, 100) {
		t.Errorf("empty window [100,100) should never fire")
	}
}

func TestPredicate_Window_InvertedRange(t *testing.T) {
	state := baseState()
	// opens_at > closes_at — malformed. Fail closed.
	pred := map[string]any{"kind": "window", "opens_at": 200, "closes_at": 100}
	if EvaluateThookPredicate(pred, state, 150) {
		t.Errorf("inverted window should fail closed (malformed)")
	}
}

// ----------------------------------------------------------------------------
// expires_at — currentT >= t, semantic "permanent closure"
// ----------------------------------------------------------------------------

func TestPredicate_ExpiresAt_NotYet(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "expires_at", "t": 300}
	if EvaluateThookPredicate(pred, state, 299) {
		t.Errorf("expires_at=300 should NOT fire at currentT=299")
	}
}

func TestPredicate_ExpiresAt_AtExactly(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "expires_at", "t": 300}
	if !EvaluateThookPredicate(pred, state, 300) {
		t.Errorf("expires_at=300 should fire at currentT=300 (inclusive)")
	}
}

func TestPredicate_ExpiresAt_After(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "expires_at", "t": 300}
	if !EvaluateThookPredicate(pred, state, 1000) {
		t.Errorf("expires_at=300 should still fire at currentT=1000")
	}
}

// ----------------------------------------------------------------------------
// on_prop_set — property presence / absence gate
// ----------------------------------------------------------------------------

func TestPredicate_OnPropSet_PropPresent(t *testing.T) {
	state := baseState()
	// Default expected=set → target has property → true.
	pred := map[string]any{
		"kind": "on_prop_set",
		"urn":  "urn:moos:program:sam.p",
		"prop": "status",
	}
	if !EvaluateThookPredicate(pred, state, 0) {
		t.Errorf("on_prop_set should be true when property is present (expected=set default)")
	}
}

func TestPredicate_OnPropSet_PropAbsent(t *testing.T) {
	state := baseState()
	pred := map[string]any{
		"kind": "on_prop_set",
		"urn":  "urn:moos:program:sam.p",
		"prop": "nonexistent",
	}
	if EvaluateThookPredicate(pred, state, 0) {
		t.Errorf("on_prop_set should be false when property is absent (expected=set default)")
	}
}

func TestPredicate_OnPropSet_ExpectedUnset(t *testing.T) {
	state := baseState()
	// expected=unset → inverts.
	pred := map[string]any{
		"kind":     "on_prop_set",
		"urn":      "urn:moos:program:sam.p",
		"prop":     "nonexistent",
		"expected": "unset",
	}
	if !EvaluateThookPredicate(pred, state, 0) {
		t.Errorf("on_prop_set with expected=unset should be true when property is absent")
	}

	pred2 := map[string]any{
		"kind":     "on_prop_set",
		"urn":      "urn:moos:program:sam.p",
		"prop":     "status",
		"expected": "unset",
	}
	if EvaluateThookPredicate(pred2, state, 0) {
		t.Errorf("on_prop_set with expected=unset should be false when property is present")
	}
}

func TestPredicate_OnPropSet_MissingNode(t *testing.T) {
	state := baseState()
	// Nonexistent target node → false (can't tell, fail-closed).
	pred := map[string]any{
		"kind": "on_prop_set",
		"urn":  "urn:moos:program:sam.nope",
		"prop": "status",
	}
	if EvaluateThookPredicate(pred, state, 0) {
		t.Errorf("on_prop_set should be false when target node does not exist")
	}
}

// ----------------------------------------------------------------------------
// when_capability — actor's session occupant has the named capability via WF02
// ----------------------------------------------------------------------------

// capState builds a state with sam having superadmin, occupying a session.
func capState() *graph.GraphState {
	return &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:user:sam": {
				URN: "urn:moos:user:sam", TypeID: "user",
				Properties: map[string]graph.Property{"name": {Value: "sam", Mutability: "immutable"}},
			},
			"urn:moos:role:superadmin": {
				URN: "urn:moos:role:superadmin", TypeID: "role",
				Properties: map[string]graph.Property{"name": {Value: "superadmin", Mutability: "immutable"}},
			},
			"urn:moos:session:sam.hp-laptop": {
				URN: "urn:moos:session:sam.hp-laptop", TypeID: "session",
				Properties: map[string]graph.Property{"seat_role": {Value: "occupier", Mutability: "mutable"}},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"r1": {
				URN: "r1", RewriteCategory: "WF19",
				SrcURN: "urn:moos:session:sam.hp-laptop", SrcPort: "has-occupant",
				TgtURN: "urn:moos:user:sam", TgtPort: "is-occupant-of",
			},
			"r2": {
				URN: "r2", RewriteCategory: "WF02",
				SrcURN: "urn:moos:user:sam", SrcPort: "governs",
				TgtURN: "urn:moos:role:superadmin", TgtPort: "governed-by",
			},
		},
	}
}

func TestPredicate_WhenCapability_Hit(t *testing.T) {
	state := capState()
	ctx := EvalContext{SessionURN: "urn:moos:session:sam.hp-laptop"}
	pred := map[string]any{"kind": "when_capability", "cap_urn": "urn:moos:role:superadmin"}
	if !EvaluateThookPredicateWithContext(pred, state, 0, ctx) {
		t.Errorf("when_capability should fire when session occupant has the WF02 cap_urn")
	}
}

func TestPredicate_WhenCapability_MissingSession(t *testing.T) {
	state := capState()
	// Empty SessionURN in context → fail-closed.
	ctx := EvalContext{}
	pred := map[string]any{"kind": "when_capability", "cap_urn": "urn:moos:role:superadmin"}
	if EvaluateThookPredicateWithContext(pred, state, 0, ctx) {
		t.Errorf("when_capability should fail-closed when ctx.SessionURN is empty")
	}
}

func TestPredicate_WhenCapability_MissingCapURN(t *testing.T) {
	state := capState()
	ctx := EvalContext{SessionURN: "urn:moos:session:sam.hp-laptop"}
	pred := map[string]any{"kind": "when_capability"} // no cap_urn
	if EvaluateThookPredicateWithContext(pred, state, 0, ctx) {
		t.Errorf("when_capability should fail-closed when cap_urn is missing")
	}
}

func TestPredicate_WhenCapability_NoWF02(t *testing.T) {
	state := capState()
	// Drop the WF02 relation — occupant no longer has the cap.
	delete(state.Relations, "r2")
	ctx := EvalContext{SessionURN: "urn:moos:session:sam.hp-laptop"}
	pred := map[string]any{"kind": "when_capability", "cap_urn": "urn:moos:role:superadmin"}
	if EvaluateThookPredicateWithContext(pred, state, 0, ctx) {
		t.Errorf("when_capability should be false when occupant has no WF02 cap link")
	}
}

func TestPredicate_WhenCapability_UnoccupiedSession(t *testing.T) {
	state := capState()
	// Drop the has-occupant relation — session is unoccupied.
	delete(state.Relations, "r1")
	ctx := EvalContext{SessionURN: "urn:moos:session:sam.hp-laptop"}
	pred := map[string]any{"kind": "when_capability", "cap_urn": "urn:moos:role:superadmin"}
	if EvaluateThookPredicateWithContext(pred, state, 0, ctx) {
		t.Errorf("when_capability should be false for unoccupied session")
	}
}

// TestPredicate_WhenCapability_InAllOf — the compound predicate composes
// with the new kind correctly. a t_hook declaring all_of(fires_at, when_capability)
// fires only when BOTH clauses hold.
func TestPredicate_WhenCapability_InAllOf(t *testing.T) {
	state := capState()
	ctx := EvalContext{SessionURN: "urn:moos:session:sam.hp-laptop"}
	pred := map[string]any{
		"kind": "all_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 100},
			map[string]any{"kind": "when_capability", "cap_urn": "urn:moos:role:superadmin"},
		},
	}
	if !EvaluateThookPredicateWithContext(pred, state, 200, ctx) {
		t.Errorf("compound all_of(fires_at, when_capability) should fire when both hold")
	}
	if EvaluateThookPredicateWithContext(pred, state, 50, ctx) {
		t.Errorf("compound should fail when fires_at clause unmet")
	}
}

// TestPredicate_BackwardCompat_NoContextForOldKinds — existing kinds still
// work via EvaluateThookPredicate without explicit context.
func TestPredicate_BackwardCompat_NoContextForOldKinds(t *testing.T) {
	state := baseState()
	pred := map[string]any{"kind": "fires_at", "t": 100}
	if !EvaluateThookPredicate(pred, state, 200) {
		t.Errorf("fires_at must still work via the context-less entry point")
	}
}
