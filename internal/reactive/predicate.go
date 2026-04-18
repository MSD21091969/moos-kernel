package reactive

import (
	"encoding/json"
	"fmt"

	"moos/kernel/internal/graph"
)

// ThookPredicate is the evaluable shape stored in t_hook.predicate (and in
// future gate / view_filter nodes). Discriminated by Kind.
//
// Supported kinds (§M14 catalog subset):
//
//	fires_at       : {kind: "fires_at",    t: N}                            — currentT >= N
//	closes_at      : {kind: "closes_at",   t: N}                            — identical to fires_at (semantic pair)
//	expires_at     : {kind: "expires_at",  t: N}                            — currentT >= N, semantic "permanent closure"
//	window         : {kind: "window",      opens_at: N, closes_at: M}       — half-open: opens_at <= currentT < closes_at
//	after_urn      : {kind: "after_urn",   urn: U, prop: F, value: V}       — node U has property F equal to V
//	before_urn     : {kind: "before_urn",  urn: U, prop: F, value: V}       — inverse of after_urn (missing node/prop → true)
//	on_prop_set    : {kind: "on_prop_set", urn: U, prop: F, expected: "set"|"unset"}
//	                                                                        — property presence/absence gate (expected defaults to "set")
//	when_capability: {kind: "when_capability", cap_urn: C}                  — ctx.SessionURN's occupant holds capability C via WF02
//	all_of         : {kind: "all_of",  predicates: [...]}                   — boolean AND
//	any_of         : {kind: "any_of",  predicates: [...]}                   — boolean OR
//
// Other §M14 kinds (recurs_every, during_urn, on_event, on_role_change, nth,
// first_of_prop, duration, reopens_at) fall through the switch and evaluate
// to false until a case is added. Unknown kinds fail-closed per §M8.
//
// when_capability requires an EvalContext with SessionURN set; callers use
// EvaluateThookPredicateWithContext for that. The context-less
// EvaluateThookPredicate returns false for when_capability (fail-closed —
// no session context means we can't resolve the occupant).
type ThookPredicate struct {
	Kind string `json:"kind"`

	// fires_at / closes_at / expires_at — time-based kinds
	T int `json:"t,omitempty"`

	// window — half-open interval
	OpensAt  int `json:"opens_at,omitempty"`
	ClosesAt int `json:"closes_at,omitempty"`

	// after_urn / before_urn / on_prop_set — target node + property (+ value)
	URN      string `json:"urn,omitempty"`
	Prop     string `json:"prop,omitempty"`
	Value    any    `json:"value,omitempty"`
	Expected string `json:"expected,omitempty"` // on_prop_set: "set" (default) | "unset"

	// when_capability — capability URN the session's occupant must hold
	CapURN string `json:"cap_urn,omitempty"`

	// all_of / any_of — boolean composition
	Predicates []ThookPredicate `json:"predicates,omitempty"`
}

// EvalContext carries caller-supplied evaluation context that some predicate
// kinds need. Currently used only by when_capability (for session-occupant
// resolution), but the type is exported so future kinds (e.g. occupant-
// scoped privacy filters) can extend it without breaking callers.
//
// Zero value is safe — kinds that need a field simply fail-closed if the
// field is empty.
type EvalContext struct {
	// SessionURN is the session under whose lens the predicate is being
	// evaluated. Used by when_capability to walk has-occupant → principal
	// → WF02 → cap_urn.
	SessionURN graph.URN
}

// EvaluateThookPredicate evaluates a t_hook predicate against current state
// and a caller-supplied calendar T.
//
// This is the context-less entry point. Predicates that need runtime context
// (currently only when_capability, which needs a session URN) fail-closed
// when called through this path — use EvaluateThookPredicateWithContext
// instead for those cases.
//
// Unknown kinds, malformed predicates, and missing nodes all return false
// (fail-closed). before_urn is the one exception: a missing node means
// "hasn't happened yet" and returns true.
//
// currentT is the caller-supplied calendar-T value. The kernel itself does
// not track T — it is passed in by the t-cone renderer, a future time-driven
// sweep loop, or a manual introspection call.
func EvaluateThookPredicate(pred any, state *graph.GraphState, currentT int) bool {
	return EvaluateThookPredicateWithContext(pred, state, currentT, EvalContext{})
}

// EvaluateThookPredicateWithContext is the context-aware entry point. Use
// this when evaluating predicates that reference the evaluating session
// (when_capability) or other caller-supplied state.
//
// Callers that don't need context can use EvaluateThookPredicate instead —
// it's a thin wrapper around this function passing EvalContext{}.
func EvaluateThookPredicateWithContext(pred any, state *graph.GraphState, currentT int, ctx EvalContext) bool {
	raw, err := json.Marshal(pred)
	if err != nil {
		return false
	}
	var p ThookPredicate
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return evaluateThookPredicateParsed(p, state, currentT, ctx)
}

// evaluateThookPredicateParsed is the recursive worker — called after the
// initial JSON normalisation so sub-predicates (inside all_of / any_of) don't
// repeatedly round-trip.
func evaluateThookPredicateParsed(p ThookPredicate, state *graph.GraphState, currentT int, ctx EvalContext) bool {
	switch p.Kind {

	case "fires_at", "closes_at":
		// Time gate — fires once currentT has caught up to the target T.
		return currentT >= p.T

	case "expires_at":
		// Semantic "permanent closure" — same threshold logic as fires_at
		// but the label signals "once true, stays true forever". Useful
		// for the sweep to mark a hook as closed rather than fired.
		return currentT >= p.T

	case "window":
		// Half-open interval [opens_at, closes_at). An inverted range
		// (opens_at > closes_at) is malformed → fail closed. An empty
		// range (opens_at == closes_at) is valid and never fires.
		if p.OpensAt > p.ClosesAt {
			return false
		}
		return currentT >= p.OpensAt && currentT < p.ClosesAt

	case "after_urn":
		// State gate — target node must have property set to expected value.
		node, ok := state.Nodes[graph.URN(p.URN)]
		if !ok {
			return false
		}
		prop, ok := node.Properties[p.Prop]
		if !ok {
			return false
		}
		return propValueEquals(prop.Value, p.Value)

	case "before_urn":
		// Inverse of after_urn. "Before" means NOT yet at the expected state,
		// so a missing node or missing property also counts as "before".
		node, ok := state.Nodes[graph.URN(p.URN)]
		if !ok {
			return true
		}
		prop, ok := node.Properties[p.Prop]
		if !ok {
			return true
		}
		return !propValueEquals(prop.Value, p.Value)

	case "on_prop_set":
		// Property presence / absence gate. Default is expected="set"
		// (true if property is present). expected="unset" inverts.
		if p.URN == "" || p.Prop == "" {
			return false
		}
		node, ok := state.Nodes[graph.URN(p.URN)]
		if !ok {
			return false // can't tell — fail closed
		}
		_, present := node.Properties[p.Prop]
		switch p.Expected {
		case "", "set":
			return present
		case "unset":
			return !present
		}
		return false // unknown expected value → fail closed

	case "when_capability":
		// Requires ctx.SessionURN. Walks:
		//   session --has-occupant--> principal
		//   principal --WF02 governs--> cap_urn
		// Returns true iff the whole chain resolves to cap_urn.
		if ctx.SessionURN == "" || p.CapURN == "" {
			return false
		}
		if _, ok := state.Nodes[ctx.SessionURN]; !ok {
			return false
		}
		// Resolve occupant via has-occupant relation (WF19 port name).
		var occupant graph.URN
		for _, rel := range state.Relations {
			if rel.SrcURN == ctx.SessionURN && rel.SrcPort == "has-occupant" {
				occupant = rel.TgtURN
				break
			}
		}
		if occupant == "" {
			return false
		}
		// Walk WF02 governs from occupant to cap_urn.
		capURN := graph.URN(p.CapURN)
		for _, rel := range state.Relations {
			if rel.SrcURN == occupant && rel.SrcPort == "governs" && rel.TgtURN == capURN {
				// Also require the target node to exist (stale link fails closed).
				if _, ok := state.Nodes[capURN]; !ok {
					return false
				}
				return true
			}
		}
		return false

	case "all_of":
		// Boolean AND — empty list trivially true (vacuous AND).
		for _, sub := range p.Predicates {
			if !evaluateThookPredicateParsed(sub, state, currentT, ctx) {
				return false
			}
		}
		return true

	case "any_of":
		// Boolean OR — empty list trivially false (vacuous OR).
		for _, sub := range p.Predicates {
			if evaluateThookPredicateParsed(sub, state, currentT, ctx) {
				return true
			}
		}
		return false
	}

	// Unknown kind — fail-closed.
	return false
}

// propValueEquals compares two property values after normalising via
// fmt.Sprintf("%v", ...). Handles the common case that JSON round-trip
// turns integers into float64 — "%v" on float64(220) is "220", same as
// "%v" on int(220), so equality survives.
//
// Strict-type comparison would require reflect + type coercion rules; we
// deliberately use string-form equality because predicate values are
// almost always enum strings or small integers, and string-form is the
// format both the log and the state renderer agree on.
func propValueEquals(actual, expected any) bool {
	return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
}
