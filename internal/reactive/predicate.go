package reactive

import (
	"encoding/json"
	"fmt"

	"moos/kernel/internal/graph"
)

// This file holds the evaluator for t_hook predicates (§M14 catalog subset).
//
// A t_hook's `predicate` property is stored as free-form JSON on the node.
// After log replay the field arrives as `map[string]any` (Go json's default
// container). The evaluator dispatches on the `kind` key.
//
// Supported kinds:
//
//	fires_at        : {kind: "fires_at",    t: N}                            — currentT >= N
//	closes_at       : {kind: "closes_at",   t: N}                            — currentT >= N (label-distinct from fires_at)
//	expires_at      : {kind: "expires_at",  t: N}                            — currentT >= N (semantic "permanent closure")
//	window          : {kind: "window",      opens_at: N, closes_at: M}       — half-open: opens_at <= currentT < closes_at
//	after_urn       : {kind: "after_urn",   urn: U, prop: F, value: V}       — node U has property F equal to V
//	before_urn      : {kind: "before_urn",  urn: U, prop: F, value: V}       — inverse of after_urn (missing node/prop → true)
//	on_prop_set     : {kind: "on_prop_set", urn: U, prop: F, expected: "set"|"unset"}
//	                                                                         — property presence/absence gate (expected defaults to "set")
//	when_capability : {kind: "when_capability", cap_urn: C}                  — ctx.SessionURN's occupant holds capability C via WF02
//	all_of          : {kind: "all_of", predicates: [...]}                    — boolean AND; explicit empty list is vacuously true
//	any_of          : {kind: "any_of", predicates: [...]}                    — boolean OR;  explicit empty list is vacuously false
//
// Fail-closed contract (§M8 stance):
//   - Unknown kinds → false
//   - Missing required field for a kind → false
//   - Malformed structural shape (wrong type on a required field) → false
//   - before_urn is the ONLY case where "missing node" or "missing property"
//     maps to true (semantics: "hasn't happened yet"). A malformed before_urn
//     predicate itself (missing urn, prop, or value) still fails closed.
//   - when_capability requires ctx.SessionURN; absent context → false.
//
// Other §M14 kinds (recurs_every, during_urn, reopens_at, on_event,
// on_role_change, nth, first_of_prop, duration) fall through the switch
// and evaluate to false until a case is added.

// EvalContext carries caller-supplied evaluation context that some predicate
// kinds need. Currently used only by when_capability for session-occupant
// resolution, but exported so future kinds can extend it without breaking
// callers. Zero value is safe — kinds that need a field fail-closed when
// the field is empty.
type EvalContext struct {
	// SessionURN is the session under whose lens the predicate is being
	// evaluated. Used by when_capability to walk has-occupant → principal
	// → WF02 → cap_urn.
	SessionURN graph.URN
}

// EvaluateThookPredicate evaluates a t_hook predicate against current state
// and a caller-supplied calendar-T. See the file header for supported kinds
// and the fail-closed contract.
//
// This is the context-less entry point. Predicates that need runtime context
// (currently only when_capability, which needs a session URN) fail-closed
// when called through this path — use EvaluateThookPredicateWithContext
// instead for those cases.
//
// `pred` is typically the raw value read from `t_hook.Properties["predicate"].Value`.
// After the kernel's log round-trip this arrives as `map[string]any`, which
// hits a fast-path (no JSON re-encoding). Any other shape (a typed struct
// built in Go, a json.RawMessage, etc.) is normalised via a JSON round-trip
// into a map and then evaluated.
//
// currentT is caller-supplied; the kernel itself does not track calendar-T.
func EvaluateThookPredicate(pred any, state *graph.GraphState, currentT int) bool {
	return EvaluateThookPredicateWithContext(pred, state, currentT, EvalContext{})
}

// EvaluateThookPredicateWithContext is the context-aware entry point. Use
// this when evaluating predicates that reference the evaluating session
// (when_capability) or other caller-supplied state.
func EvaluateThookPredicateWithContext(pred any, state *graph.GraphState, currentT int, ctx EvalContext) bool {
	// Fast-path: the common case (predicate read from a node's properties
	// after log replay is already a map[string]any).
	if m, ok := pred.(map[string]any); ok {
		return evaluateMapWithContext(m, state, currentT, ctx)
	}
	// Fallback: normalise to map[string]any via a JSON round-trip.
	raw, err := json.Marshal(pred)
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return evaluateMapWithContext(m, state, currentT, ctx)
}

// EvaluateThookPredicateMap is the performance-optimised variant for callers
// who already hold a parsed predicate. Exported so the sweep loop and t-cone
// renderer can skip the type-switch-and-normalise dance on hot paths.
//
// Returns false on any malformed input per the fail-closed contract.
// Predicates requiring context (when_capability) fail-closed — callers that
// need context should use the ...WithContext form.
func EvaluateThookPredicateMap(p map[string]any, state *graph.GraphState, currentT int) bool {
	return evaluateMapWithContext(p, state, currentT, EvalContext{})
}

// evaluateMapWithContext is the internal dispatcher shared by both the
// context-less and context-aware entry points.
func evaluateMapWithContext(p map[string]any, state *graph.GraphState, currentT int, ctx EvalContext) bool {
	kind, ok := p["kind"].(string)
	if !ok || kind == "" {
		return false
	}

	switch kind {

	case "fires_at", "closes_at", "expires_at":
		// Time gate — fires once currentT catches up. Fail-closed if `t`
		// is missing or not a number. `expires_at` shares threshold logic
		// with fires_at but semantically signals "permanent closure"
		// (useful for the sweep to mark a hook as closed rather than proposed
		// once firing_state lands in a later ontology bump).
		tRaw, has := p["t"]
		if !has {
			return false
		}
		t, ok := coerceInt(tRaw)
		if !ok {
			return false
		}
		return currentT >= t

	case "window":
		// Half-open interval [opens_at, closes_at). Inverted range
		// (opens_at > closes_at) is malformed → fail closed. Empty range
		// (opens_at == closes_at) is valid and never fires.
		oRaw, hasO := p["opens_at"]
		cRaw, hasC := p["closes_at"]
		if !hasO || !hasC {
			return false
		}
		opensAt, ok1 := coerceInt(oRaw)
		closesAt, ok2 := coerceInt(cRaw)
		if !ok1 || !ok2 {
			return false
		}
		if opensAt > closesAt {
			return false
		}
		return currentT >= opensAt && currentT < closesAt

	case "on_prop_set":
		// Property presence / absence gate. Default expected="set".
		urn, ok1 := p["urn"].(string)
		prop, ok2 := p["prop"].(string)
		if !ok1 || !ok2 || urn == "" || prop == "" {
			return false
		}
		node, nodeOK := state.Nodes[graph.URN(urn)]
		if !nodeOK {
			return false // can't tell — fail closed
		}
		_, present := node.Properties[prop]
		expected, _ := p["expected"].(string)
		switch expected {
		case "", "set":
			return present
		case "unset":
			return !present
		}
		return false // unknown expected value → fail closed

	case "when_capability":
		// ctx.SessionURN --has-occupant--> principal --WF02 governs--> cap_urn
		// Missing context, cap_urn, non-session context URN, stale occupant,
		// or missing WF02 chain → false. Tightened vs. original (PR #16
		// review — Copilot):
		//   - Verify ctx.SessionURN is actually a type_id=="session" node.
		//   - Match BOTH ports (has-occupant / is-occupant-of) so the walk
		//     doesn't trip on unrelated links that happen to reuse one port name.
		//   - Verify the occupant target node exists in state (stale LINK
		//     otherwise grants capability via a deleted principal).
		//   - Verify the cap_urn target node exists.
		capURN, hasCap := p["cap_urn"].(string)
		if !hasCap || capURN == "" || ctx.SessionURN == "" {
			return false
		}
		sessionNode, ok := state.Nodes[ctx.SessionURN]
		if !ok || sessionNode.TypeID != "session" {
			return false
		}
		// Walk only the relations outbound from the session (O(edges-at-
		// session) on indexed state; falls back to scan otherwise).
		var occupant graph.URN
		for _, relURN := range state.RelationsFrom(ctx.SessionURN) {
			rel, ok := state.Relations[relURN]
			if !ok {
				continue
			}
			if rel.SrcPort != "has-occupant" || rel.TgtPort != "is-occupant-of" {
				continue
			}
			if _, ok := state.Nodes[rel.TgtURN]; !ok {
				continue // stale LINK; skip
			}
			occupant = rel.TgtURN
			break
		}
		if occupant == "" {
			return false
		}
		target := graph.URN(capURN)
		if _, ok := state.Nodes[target]; !ok {
			return false
		}
		// Walk only relations outbound from the occupant.
		for _, relURN := range state.RelationsFrom(occupant) {
			rel, ok := state.Relations[relURN]
			if !ok {
				continue
			}
			if rel.SrcURN == occupant && rel.SrcPort == "governs" && rel.TgtURN == target {
				return true
			}
		}
		return false

	case "after_urn":
		// State gate — target node has property equal to expected value.
		// Requires urn, prop, and value to all be present and well-typed.
		urn, ok1 := p["urn"].(string)
		prop, ok2 := p["prop"].(string)
		val, hasVal := p["value"]
		if !ok1 || !ok2 || !hasVal || urn == "" || prop == "" {
			return false
		}
		node, ok := state.Nodes[graph.URN(urn)]
		if !ok {
			return false
		}
		propRecord, ok := node.Properties[prop]
		if !ok {
			return false
		}
		return propValueEquals(propRecord.Value, val)

	case "before_urn":
		// Inverse of after_urn. A MALFORMED predicate still fails closed.
		// A WELL-FORMED predicate with a missing node or missing property
		// means "target hasn't reached the state yet" and returns true.
		urn, ok1 := p["urn"].(string)
		prop, ok2 := p["prop"].(string)
		val, hasVal := p["value"]
		if !ok1 || !ok2 || !hasVal || urn == "" || prop == "" {
			return false
		}
		node, ok := state.Nodes[graph.URN(urn)]
		if !ok {
			return true // node not yet in graph → "before"
		}
		propRecord, ok := node.Properties[prop]
		if !ok {
			return true // property not yet set → "before"
		}
		return !propValueEquals(propRecord.Value, val)

	case "all_of":
		// Boolean AND. `predicates` must be present and be an array.
		// An explicit empty array is vacuously true. Context threads
		// through to nested when_capability clauses.
		arr, ok := extractPredicates(p)
		if !ok {
			return false
		}
		for _, sub := range arr {
			if !evaluateMapWithContext(sub, state, currentT, ctx) {
				return false
			}
		}
		return true

	case "any_of":
		// Boolean OR. Missing `predicates` fails closed; explicit empty
		// array is vacuously false.
		arr, ok := extractPredicates(p)
		if !ok {
			return false
		}
		for _, sub := range arr {
			if evaluateMapWithContext(sub, state, currentT, ctx) {
				return true
			}
		}
		return false
	}

	// Unknown kind — fail closed (§M8).
	return false
}

// extractPredicates reads the `predicates` field of an all_of/any_of
// predicate into a []map[string]any. Returns (nil, false) if the field is
// missing, not an array, or contains non-object elements.
//
// An explicit empty array is returned as a non-nil empty slice with ok=true,
// which lets callers distinguish "predicates: []" (vacuous) from "no
// predicates field at all" (malformed).
func extractPredicates(p map[string]any) ([]map[string]any, bool) {
	raw, has := p["predicates"]
	if !has {
		return nil, false
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(arr))
	for _, sub := range arr {
		m, ok := sub.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

// coerceInt converts a JSON-decoded number to int. JSON numbers default to
// float64; int/int64/json.Number are also handled for safety with callers
// that use the json.Number decoder option.
func coerceInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		if f, err := x.Float64(); err == nil {
			return int(f), true
		}
		return 0, false
	}
	return 0, false
}

// propValueEquals compares two property values after normalising via
// fmt.Sprintf("%v", ...). Handles the common case that JSON round-trip
// turns integers into float64 — "%v" on float64(220) is "220", same as
// "%v" on int(220), so equality survives.
//
// Strict-type comparison would require reflect + type coercion rules; we
// deliberately use string-form equality because predicate values are
// almost always enum strings or small integers, and string-form is the
// format both the log and the state renderer agree on. The allocation cost
// is acceptable — predicates evaluate at most once per sweep tick per hook,
// not per rewrite.
func propValueEquals(actual, expected any) bool {
	return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
}
