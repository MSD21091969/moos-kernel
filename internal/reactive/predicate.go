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
//	fires_at   : {kind: "fires_at",   t: N}                   — currentT >= N
//	closes_at  : {kind: "closes_at",  t: N}                   — currentT >= N (label-distinct from fires_at)
//	after_urn  : {kind: "after_urn",  urn: U, prop: F, value: V}
//	                                                           — node U has property F equal to V
//	before_urn : {kind: "before_urn", urn: U, prop: F, value: V}
//	                                                           — inverse of after_urn; missing node/prop reads as "before" (true)
//	all_of     : {kind: "all_of", predicates: [...]}          — boolean AND; explicit empty list is vacuously true
//	any_of     : {kind: "any_of", predicates: [...]}          — boolean OR;  explicit empty list is vacuously false
//
// Fail-closed contract (§M8 stance):
//   - Unknown kinds → false
//   - Missing required field for a kind → false
//   - Malformed structural shape (wrong type on a required field) → false
//   - before_urn is the ONLY case where "missing node" or "missing property"
//     maps to true (semantics: "hasn't happened yet"). A malformed before_urn
//     predicate itself (missing urn, prop, or value) still fails closed.
//
// Other §M14 kinds (window, recurs_every, during_urn, expires_at, reopens_at,
// on_event, on_prop_set, on_role_change, when_capability, nth, first_of_prop,
// duration) are not yet implemented — they fall through the switch and
// evaluate to false until a case is added.

// EvaluateThookPredicate evaluates a t_hook predicate against current state
// and a caller-supplied calendar-T. See the file header for supported kinds
// and the fail-closed contract.
//
// `pred` is typically the raw value read from `t_hook.Properties["predicate"].Value`.
// After the kernel's log round-trip this arrives as `map[string]any`, which
// hits a fast-path (no JSON re-encoding). Any other shape (a typed struct
// built in Go, a json.RawMessage, etc.) is normalised via a JSON round-trip
// into a map and then evaluated.
//
// currentT is caller-supplied; the kernel itself does not track calendar-T.
func EvaluateThookPredicate(pred any, state *graph.GraphState, currentT int) bool {
	// Fast-path: the common case (predicate read from a node's properties
	// after log replay is already a map[string]any).
	if m, ok := pred.(map[string]any); ok {
		return EvaluateThookPredicateMap(m, state, currentT)
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
	return EvaluateThookPredicateMap(m, state, currentT)
}

// EvaluateThookPredicateMap is the performance-optimised variant for callers
// who already hold a parsed predicate. Exported so the sweep loop and t-cone
// renderer can skip the type-switch-and-normalise dance on hot paths.
//
// Returns false on any malformed input per the fail-closed contract.
func EvaluateThookPredicateMap(p map[string]any, state *graph.GraphState, currentT int) bool {
	kind, ok := p["kind"].(string)
	if !ok || kind == "" {
		return false
	}

	switch kind {

	case "fires_at", "closes_at":
		// Time gate — fires once currentT catches up. Fail-closed if `t`
		// is missing or not a number (prior implementation defaulted to 0,
		// which meant any non-negative currentT triggered firing).
		tRaw, has := p["t"]
		if !has {
			return false
		}
		t, ok := coerceInt(tRaw)
		if !ok {
			return false
		}
		return currentT >= t

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
		// An explicit empty array is vacuously true.
		arr, ok := extractPredicates(p)
		if !ok {
			return false
		}
		for _, sub := range arr {
			if !EvaluateThookPredicateMap(sub, state, currentT) {
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
			if EvaluateThookPredicateMap(sub, state, currentT) {
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
