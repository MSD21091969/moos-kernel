package transport

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/reactive"
)

// handleGetTHookEvaluate evaluates a t_hook's predicate against current state
// and a caller-supplied calendar-T.
//
// Route: GET /t-hook/evaluate/{urn}?at=<T>
//
// Query parameter:
//
//	at    integer T-day to evaluate at. Omit to default to currentTDay()
//	      (the kernel's wall-clock-derived T).
//
// Response shape (200 OK):
//
//	{
//	  "urn":            "urn:moos:t_hook:...",
//	  "at_t":           220,
//	  "fires":          true,
//	  "predicate":      {kind: "...", ...},
//	  "owner_urn":      "urn:moos:program:...",
//	  "react_template": {rewrite_type: "MUTATE", ...}
//	}
//
// Error cases:
//
//	404 — t_hook node not found
//	400 — node exists but is not of type t_hook
//	400 — invalid "at" query parameter
//	422 — t_hook has no predicate property, or the predicate value is nil
//
// The predicate evaluation itself is delegated to
// reactive.EvaluateThookPredicate. See internal/reactive/predicate.go for
// the semantics of each supported predicate kind (fires_at, closes_at,
// after_urn, before_urn, all_of, any_of; unknown kinds fail-closed).
func (s *Server) handleGetTHookEvaluate(w http.ResponseWriter, r *http.Request) {
	urn := graph.URN(r.PathValue("urn"))

	// Validate the node first via the cheap single-node lookup; only fetch
	// the full graph state once we're sure we have a well-formed t_hook to
	// evaluate. This avoids an O(N) state clone on requests that will 404
	// or 400 anyway.
	node, ok := s.inspect.Node(urn)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("t_hook not found: %s", urn))
		return
	}
	if node.TypeID != "t_hook" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("node %s is not a t_hook (type=%s)", urn, node.TypeID))
		return
	}

	// A t_hook without a predicate (field missing OR explicit nil value) has
	// nothing to evaluate. Both cases return 422 for consistency.
	predProp, hasPred := node.Properties["predicate"]
	if !hasPred || predProp.Value == nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("t_hook %s has no predicate value — nothing to evaluate", urn))
		return
	}

	// at=T query parameter; default to currentTDay().
	atT := currentTDay()
	if raw := strings.TrimSpace(r.URL.Query().Get("at")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid at query parameter: must be integer T-day")
			return
		}
		atT = parsed
	}

	// Only fetch the full state when we're about to use it. Predicates like
	// after_urn/before_urn read other nodes by URN.
	state := s.inspect.State()
	fires := reactive.EvaluateThookPredicate(predProp.Value, &state, atT)

	resp := map[string]any{
		"urn":       urn,
		"at_t":      atT,
		"fires":     fires,
		"predicate": predProp.Value,
	}

	// Include owner + react_template for operator convenience (lets the
	// caller see, at a glance, what this hook would do if it fires).
	if ownerProp, ok := node.Properties["owner_urn"]; ok {
		resp["owner_urn"] = ownerProp.Value
	}
	if reactProp, ok := node.Properties["react_template"]; ok {
		resp["react_template"] = reactProp.Value
	}

	writeJSON(w, http.StatusOK, resp)
}
