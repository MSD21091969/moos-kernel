package reactive

import (
	"testing"

	"moos/kernel/internal/graph"
)

// stateForPredicates returns a small state with two programs that the
// compound-startable tests can reason about.
func stateForPredicates() *graph.GraphState {
	return &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.t187-kernel-proper": {
				URN:    "urn:moos:program:sam.t187-kernel-proper",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status":   {Value: "active", Mutability: "mutable"},
					"starts_t": {Value: 187, Mutability: "mutable"},
					"target_t": {Value: 220, Mutability: "mutable"},
				},
			},
			"urn:moos:program:sam.v310-delivery": {
				URN:    "urn:moos:program:sam.v310-delivery",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status":   {Value: "draft", Mutability: "mutable"},
					"starts_t": {Value: 220, Mutability: "mutable"},
					"target_t": {Value: 240, Mutability: "mutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
}

// TestThookPredicate_FiresAt — fires_at is true once currentT catches up.
func TestThookPredicate_FiresAt(t *testing.T) {
	state := stateForPredicates()
	pred := map[string]any{"kind": "fires_at", "t": 187}

	if EvaluateThookPredicate(pred, state, 168) {
		t.Error("fires_at=187 at T=168 should be false")
	}
	if EvaluateThookPredicate(pred, state, 186) {
		t.Error("fires_at=187 at T=186 should be false")
	}
	if !EvaluateThookPredicate(pred, state, 187) {
		t.Error("fires_at=187 at T=187 should be true (inclusive)")
	}
	if !EvaluateThookPredicate(pred, state, 300) {
		t.Error("fires_at=187 at T=300 should still be true")
	}
}

// TestThookPredicate_ClosesAt — closes_at has identical semantics to fires_at
// (distinct label, shared threshold logic — paired-with-fires_at use case).
func TestThookPredicate_ClosesAt(t *testing.T) {
	state := stateForPredicates()
	pred := map[string]any{"kind": "closes_at", "t": 220}

	if EvaluateThookPredicate(pred, state, 219) {
		t.Error("closes_at=220 at T=219 should be false")
	}
	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("closes_at=220 at T=220 should be true")
	}
}

// TestThookPredicate_AfterURN — after_urn is true when the target node has
// the expected property value.
func TestThookPredicate_AfterURN(t *testing.T) {
	state := stateForPredicates()

	// t187-kernel-proper.status currently "active" — predicate looks for "completed".
	pred := map[string]any{
		"kind":  "after_urn",
		"urn":   "urn:moos:program:sam.t187-kernel-proper",
		"prop":  "status",
		"value": "completed",
	}

	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("after_urn(kernel-proper.status=completed) should be false when status=active")
	}

	// Mutate status to completed — predicate should now pass.
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("after_urn(kernel-proper.status=completed) should be true when status=completed")
	}

	// Missing node → false.
	missingPred := map[string]any{
		"kind": "after_urn", "urn": "urn:moos:program:sam.nonexistent",
		"prop": "status", "value": "completed",
	}
	if EvaluateThookPredicate(missingPred, state, 220) {
		t.Error("after_urn on missing node should be false")
	}

	// Missing property → false.
	missingProp := map[string]any{
		"kind": "after_urn", "urn": "urn:moos:program:sam.v310-delivery",
		"prop": "completed_t", "value": 240,
	}
	if EvaluateThookPredicate(missingProp, state, 220) {
		t.Error("after_urn with missing property should be false")
	}
}

// TestThookPredicate_BeforeURN — inverse of after_urn. Missing node means
// "hasn't happened yet" which is "before" — returns true.
func TestThookPredicate_BeforeURN(t *testing.T) {
	state := stateForPredicates()

	// Status is "active", not "completed" — so "before completed" should be true.
	pred := map[string]any{
		"kind":  "before_urn",
		"urn":   "urn:moos:program:sam.t187-kernel-proper",
		"prop":  "status",
		"value": "completed",
	}
	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("before_urn should be true when status=active != completed")
	}

	// Missing node → still "before" = true.
	missingPred := map[string]any{
		"kind": "before_urn", "urn": "urn:moos:program:sam.not-yet",
		"prop": "status", "value": "completed",
	}
	if !EvaluateThookPredicate(missingPred, state, 220) {
		t.Error("before_urn on missing node should be true (hasn't happened)")
	}

	// Once status transitions to "completed", before_urn flips to false.
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("before_urn should be false once status=completed")
	}
}

// TestThookPredicate_AllOf — boolean AND short-circuits correctly.
func TestThookPredicate_AllOf(t *testing.T) {
	state := stateForPredicates()

	pred := map[string]any{
		"kind": "all_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 220},
			map[string]any{
				"kind": "after_urn",
				"urn":  "urn:moos:program:sam.t187-kernel-proper",
				"prop": "status", "value": "completed",
			},
		},
	}

	// T=200 < 220 AND status=active != completed → both false → AND false.
	if EvaluateThookPredicate(pred, state, 200) {
		t.Error("all_of with both sub-false should be false")
	}

	// T=220 but status still active → AND false (second fails).
	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("all_of with T satisfied but status pending should be false")
	}

	// Flip status → completed. Now both true → AND true.
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("all_of with both sub-true should be true")
	}

	// But T=219 still fails the time clause.
	if EvaluateThookPredicate(pred, state, 219) {
		t.Error("all_of with time unmet should be false even if state is satisfied")
	}

	// Empty all_of is vacuously true.
	emptyAllOf := map[string]any{"kind": "all_of", "predicates": []any{}}
	if !EvaluateThookPredicate(emptyAllOf, state, 220) {
		t.Error("empty all_of should be vacuously true")
	}
}

// TestThookPredicate_AnyOf — boolean OR short-circuits correctly.
func TestThookPredicate_AnyOf(t *testing.T) {
	state := stateForPredicates()

	pred := map[string]any{
		"kind": "any_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 500}, // far in the future
			map[string]any{
				"kind": "after_urn",
				"urn":  "urn:moos:program:sam.t187-kernel-proper",
				"prop": "status", "value": "active",
			},
		},
	}

	// Time clause far out, but status IS active → OR true.
	if !EvaluateThookPredicate(pred, state, 168) {
		t.Error("any_of with second sub-true should be true")
	}

	// Empty any_of is vacuously false.
	emptyAnyOf := map[string]any{"kind": "any_of", "predicates": []any{}}
	if EvaluateThookPredicate(emptyAnyOf, state, 220) {
		t.Error("empty any_of should be vacuously false")
	}
}

// TestThookPredicate_NestedComposition — all_of of any_of of fires_at + after_urn,
// the kind of shape a future skill-gated delivery hook might need.
func TestThookPredicate_NestedComposition(t *testing.T) {
	state := stateForPredicates()

	pred := map[string]any{
		"kind": "all_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 220},
			map[string]any{
				"kind": "any_of",
				"predicates": []any{
					map[string]any{
						"kind": "after_urn",
						"urn":  "urn:moos:program:sam.t187-kernel-proper",
						"prop": "status", "value": "completed",
					},
					map[string]any{
						"kind": "after_urn",
						"urn":  "urn:moos:program:sam.t187-kernel-proper",
						"prop": "status", "value": "archived",
					},
				},
			},
		},
	}

	// T=220, status=active → all_of's time passes, but inner any_of fails.
	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("nested: active status should fail both completed/archived branches")
	}

	// Flip to archived → inner any_of's second branch passes → everything true.
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "archived", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("nested: archived status should pass the any_of second branch")
	}
}

// TestThookPredicate_V310DeliveryStartable — replicates the exact predicate
// structure from the round-8 t_hook urn:moos:t_hook:sam.v310-delivery.startable.
// This is the canonical "dependent-program startable" shape: fires at T=220
// when t187-kernel-proper.status=completed.
func TestThookPredicate_V310DeliveryStartable(t *testing.T) {
	state := stateForPredicates()

	// Exact shape as ADDed in round 8.
	pred := map[string]any{
		"kind": "all_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 220},
			map[string]any{
				"kind": "after_urn",
				"urn":  "urn:moos:program:sam.t187-kernel-proper",
				"prop": "status", "value": "completed",
			},
		},
	}

	// Today (T=168): false — T too early, status not completed.
	if EvaluateThookPredicate(pred, state, 168) {
		t.Error("v310-delivery.startable at T=168 should be false")
	}

	// At T=220 but t187-kernel-proper not yet completed: still false (depends-on unmet).
	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("v310-delivery.startable at T=220 with kernel-proper active should be false")
	}

	// t187-kernel-proper completes early (say at T=215).
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	// T=215 still too early — fires_at=220 not met.
	if EvaluateThookPredicate(pred, state, 215) {
		t.Error("v310-delivery.startable at T=215 (T too early, even though deps done) should be false")
	}

	// T=220 and kernel-proper completed: finally fires.
	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("v310-delivery.startable at T=220 with kernel-proper=completed should be TRUE")
	}
}

// TestThookPredicate_WiringProposerStartable — the 3-clause all_of from
// urn:moos:t_hook:sam.wiring-proposer.startable. Requires T>=240 AND both
// t187-kernel-proper and v310-delivery to be completed.
func TestThookPredicate_WiringProposerStartable(t *testing.T) {
	state := stateForPredicates()

	pred := map[string]any{
		"kind": "all_of",
		"predicates": []any{
			map[string]any{"kind": "fires_at", "t": 240},
			map[string]any{
				"kind": "after_urn",
				"urn":  "urn:moos:program:sam.t187-kernel-proper",
				"prop": "status", "value": "completed",
			},
			map[string]any{
				"kind": "after_urn",
				"urn":  "urn:moos:program:sam.v310-delivery",
				"prop": "status", "value": "completed",
			},
		},
	}

	// Complete kernel-proper — still need v310-delivery.
	kp := state.Nodes["urn:moos:program:sam.t187-kernel-proper"]
	kp.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.t187-kernel-proper"] = kp

	if EvaluateThookPredicate(pred, state, 240) {
		t.Error("wiring-proposer.startable should still be false — v310-delivery not completed")
	}

	// Complete v310-delivery too.
	v3 := state.Nodes["urn:moos:program:sam.v310-delivery"]
	v3.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.v310-delivery"] = v3

	if !EvaluateThookPredicate(pred, state, 240) {
		t.Error("wiring-proposer.startable at T=240 with both deps completed should be TRUE")
	}

	// Time not yet reached — still false.
	if EvaluateThookPredicate(pred, state, 239) {
		t.Error("wiring-proposer.startable at T=239 should be false (T not met)")
	}
}

// TestThookPredicate_UnknownKindFailsClosed — unknown predicate kinds return
// false (§M8 fail-closed safety stance).
func TestThookPredicate_UnknownKindFailsClosed(t *testing.T) {
	state := stateForPredicates()

	pred := map[string]any{"kind": "hypothetical_future_kind", "stuff": 42}
	if EvaluateThookPredicate(pred, state, 220) {
		t.Error("unknown predicate kind should return false (fail-closed)")
	}

	// Also test that an empty / malformed predicate fails closed.
	if EvaluateThookPredicate(nil, state, 220) {
		t.Error("nil predicate should fail closed")
	}
	if EvaluateThookPredicate("not-an-object", state, 220) {
		t.Error("non-object predicate should fail closed")
	}
}

// TestThookPredicate_JSONNumberCoercion — after a log round-trip, integer
// predicate values come through as float64. Verify the evaluator still
// compares them correctly against int-typed node properties.
func TestThookPredicate_JSONNumberCoercion(t *testing.T) {
	state := stateForPredicates()

	// deadline_t stored as int in the property (as it would be after an ADD).
	state.Nodes["urn:moos:external_op:sam.test"] = graph.Node{
		URN:    "urn:moos:external_op:sam.test",
		TypeID: "external_op",
		Properties: map[string]graph.Property{
			"deadline_t": {Value: 187, Mutability: "mutable"},
			"status":     {Value: "pending", Mutability: "mutable"},
		},
	}

	// Predicate stored as float64 (simulating JSON round-trip).
	pred := map[string]any{
		"kind": "after_urn",
		"urn":  "urn:moos:external_op:sam.test",
		"prop": "deadline_t",
		"value": float64(187),
	}

	if !EvaluateThookPredicate(pred, state, 220) {
		t.Error("int 187 and float64 187 should compare equal under Sprintf formatting")
	}
}

// TestThookPredicate_MalformedFailClosed verifies that predicates missing
// required fields for their kind evaluate to false, rather than silently
// treating zero values as valid input. Regression for the PR #9 review
// (Copilot flagged fires_at/before_urn/all_of all defaulting to truthy on
// missing-field inputs).
func TestThookPredicate_MalformedFailClosed(t *testing.T) {
	state := stateForPredicates()

	cases := []struct {
		name string
		pred map[string]any
		at   int
	}{
		// fires_at / closes_at without `t` must fail closed (previously
		// default-zero would fire at any non-negative currentT).
		{"fires_at missing t", map[string]any{"kind": "fires_at"}, 0},
		{"fires_at missing t at high T", map[string]any{"kind": "fires_at"}, 10_000},
		{"fires_at with non-numeric t", map[string]any{"kind": "fires_at", "t": "not-a-number"}, 220},
		{"closes_at missing t", map[string]any{"kind": "closes_at"}, 220},

		// after_urn requires urn+prop+value. All absent or empty → false.
		{"after_urn missing everything", map[string]any{"kind": "after_urn"}, 220},
		{"after_urn empty urn", map[string]any{"kind": "after_urn", "urn": "", "prop": "status", "value": "completed"}, 220},
		{"after_urn empty prop", map[string]any{"kind": "after_urn", "urn": "urn:moos:program:sam.t187-kernel-proper", "prop": "", "value": "completed"}, 220},
		{"after_urn missing value", map[string]any{"kind": "after_urn", "urn": "urn:moos:program:sam.t187-kernel-proper", "prop": "status"}, 220},

		// before_urn malformed must ALSO fail closed — even though a
		// well-formed before_urn returns true for missing nodes, a
		// predicate without urn/prop/value has no ground truth to invert.
		{"before_urn missing everything", map[string]any{"kind": "before_urn"}, 220},
		{"before_urn empty urn", map[string]any{"kind": "before_urn", "urn": "", "prop": "status", "value": "completed"}, 220},
		{"before_urn empty prop", map[string]any{"kind": "before_urn", "urn": "urn:moos:program:sam.t187-kernel-proper", "prop": "", "value": "completed"}, 220},
		{"before_urn missing value", map[string]any{"kind": "before_urn", "urn": "urn:moos:program:sam.t187-kernel-proper", "prop": "status"}, 220},

		// all_of / any_of without predicates must fail closed. Vacuous
		// truth/falsity only applies to an EXPLICIT empty array.
		{"all_of without predicates", map[string]any{"kind": "all_of"}, 220},
		{"all_of with non-array predicates", map[string]any{"kind": "all_of", "predicates": "not-an-array"}, 220},
		{"all_of with non-object sub-predicate", map[string]any{"kind": "all_of", "predicates": []any{"not-an-object"}}, 220},
		{"any_of without predicates", map[string]any{"kind": "any_of"}, 220},
		{"any_of with non-array predicates", map[string]any{"kind": "any_of", "predicates": 42}, 220},

		// Missing or empty kind.
		{"no kind", map[string]any{}, 220},
		{"empty kind", map[string]any{"kind": ""}, 220},
		{"non-string kind", map[string]any{"kind": 42}, 220},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if EvaluateThookPredicate(tc.pred, state, tc.at) {
				t.Errorf("%s: expected false (fail-closed), got true", tc.name)
			}
		})
	}
}

// TestThookPredicate_ExplicitEmptyCompositions verifies that an EXPLICIT empty
// predicates array preserves the mathematical identities of AND/OR:
//
//	all_of([]) = true   (vacuous AND)
//	any_of([]) = false  (vacuous OR)
//
// This is the intentional contrast with TestThookPredicate_MalformedFailClosed:
// empty is allowed, missing is not.
func TestThookPredicate_ExplicitEmptyCompositions(t *testing.T) {
	state := stateForPredicates()

	allOfEmpty := map[string]any{"kind": "all_of", "predicates": []any{}}
	if !EvaluateThookPredicate(allOfEmpty, state, 220) {
		t.Error("all_of with explicit empty predicates should be vacuously true")
	}

	anyOfEmpty := map[string]any{"kind": "any_of", "predicates": []any{}}
	if EvaluateThookPredicate(anyOfEmpty, state, 220) {
		t.Error("any_of with explicit empty predicates should be vacuously false")
	}
}

// TestThookPredicate_MapFastPath verifies the performance fast-path: callers
// that pass an already-parsed map[string]any skip the marshal/unmarshal
// round-trip. Semantically identical to EvaluateThookPredicate but exported
// so sweep loops can avoid allocations.
func TestThookPredicate_MapFastPath(t *testing.T) {
	state := stateForPredicates()

	// Direct map input — should take the fast path.
	pred := map[string]any{"kind": "fires_at", "t": 187}

	// Both entry points should agree.
	if EvaluateThookPredicate(pred, state, 200) != EvaluateThookPredicateMap(pred, state, 200) {
		t.Error("EvaluateThookPredicate and EvaluateThookPredicateMap should produce identical results")
	}
	if !EvaluateThookPredicateMap(pred, state, 200) {
		t.Error("fires_at t=187 at currentT=200 via Map fast-path should be true")
	}
}

// TestThookPredicate_NonMapFallback verifies that non-map inputs (e.g. a
// JSON string source, a raw bytes blob) still normalise correctly via the
// JSON round-trip path.
func TestThookPredicate_NonMapFallback(t *testing.T) {
	state := stateForPredicates()

	// Input as a JSON-encoded byte slice — not a map. Must round-trip.
	// json.Marshal of a []byte produces base64, so use json.RawMessage instead.
	type Struct struct {
		Kind string `json:"kind"`
		T    int    `json:"t"`
	}
	typed := Struct{Kind: "fires_at", T: 187}

	if !EvaluateThookPredicate(typed, state, 200) {
		t.Error("fires_at from a typed Go struct should evaluate correctly via fallback path")
	}
	if EvaluateThookPredicate(typed, state, 100) {
		t.Error("fires_at from a typed Go struct at T<t should be false")
	}
}
