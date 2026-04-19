package transport

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/operad"
	"moos/kernel/internal/reactive"
)

// ----------------------------------------------------------------------------
// §M15 — t-cone projection endpoint
// ----------------------------------------------------------------------------
//
// GET /t-cone?session=<urn>&at=<T>
//
// The occupant's view of nodes with currently-open t_hooks, filtered by the
// occupant's WF02 capability scope. Sam's framing per §M15: "the admin's
// view on important programs".
//
// Algorithm:
//  1. Resolve the session URN from query. 400 if missing.
//  2. Look up the session node; verify type_id=="session" and seat_role is
//     occupier or delegate. 404 / 400 otherwise.
//  3. Resolve the occupant via WF19 has-occupant (operad.ResolveSessionOccupant).
//     An unoccupied-but-live session returns an empty cone rather than erroring.
//  4. Determine admin status: operad.CheckAdminCapability(state, occupant).
//  5. Walk all t_hook nodes; evaluate each predicate at T. For every hook
//     that fires, record the (owner_urn, hook_urn, predicate_kind) triple.
//  6. For each distinct owner_urn, load the owner node. Filter out admin-
//     scope types (governance_proposal, role, capability, ontology_publication)
//     when the occupant is not an admin.
//  7. Emit response: metadata + array of cone nodes, each with its open hooks.
//
// Response body (200 OK):
//
//	{
//	  "t":        220,
//	  "session":  "urn:moos:session:sam.hp-laptop",
//	  "occupant": "urn:moos:user:sam",
//	  "nodes": [
//	    {
//	      "urn":        "urn:moos:program:sam.t187-kernel-proper",
//	      "type_id":    "program",
//	      "properties": { ... },
//	      "open_t_hooks": [
//	        { "urn": "urn:moos:t_hook:...", "predicate_kind": "fires_at", "predicate": {...} }
//	      ]
//	    }
//	  ]
//	}
//
// Status codes:
//
//	200 — ok (includes live-but-unoccupied case, empty nodes)
//	400 — session param missing, session not a session node, or seat_role not live
//	404 — session URN does not exist
//
// Uses operad.ResolveSessionOccupant + operad.CheckAdminCapability (from M4)
// and reactive.EvaluateThookPredicate (from M1 / the pure evaluator).

// adminScopeTypes are type_ids whose instances are visible ONLY to
// admin-capable occupants. Keeps the filter simple for the MVP; a future
// refinement can walk per-type authority_scope from the registry.
var adminScopeTypes = map[graph.TypeID]struct{}{
	"governance_proposal":  {},
	"role":                 {},
	"capability":           {},
	"ontology_publication": {},
	"grammar_fragment":     {}, // admin-authored; non-admins see only status-merged copies via the canonical type
}

func (s *Server) handleGetTCone(w http.ResponseWriter, r *http.Request) {
	sessionParam := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionParam == "" {
		writeError(w, http.StatusBadRequest, "missing session query parameter")
		return
	}
	sessionURN := graph.URN(sessionParam)

	// at=T query parameter (optional; defaults to currentTDay()).
	atT := currentTDay()
	if raw := strings.TrimSpace(r.URL.Query().Get("at")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid at query parameter: must be integer T-day")
			return
		}
		atT = parsed
	}

	// Cheap lookups first — avoid fetching full state for 4xx paths.
	sessionNode, ok := s.inspect.Node(sessionURN)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", sessionURN))
		return
	}
	if sessionNode.TypeID != "session" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("URN %s is not a session (type=%s)", sessionURN, sessionNode.TypeID))
		return
	}
	// Prefer seat_role; fall back to legacy `role` (v3.10 accepts both;
	// v3.11 removes `role`). fmt.Sprint is used instead of a raw string
	// assertion because the property value may arrive as graph.URN or
	// another string-like alias depending on how it was stored — the raw
	// `.(string)` form would silently return "" and misclassify the seat
	// as not-live (PR #14 review — Gemini).
	seatRole := stringPropValue(sessionNode.Properties["seat_role"])
	if seatRole == "" {
		seatRole = stringPropValue(sessionNode.Properties["role"])
	}
	if seatRole != "occupier" && seatRole != "delegate" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("session %s is not live (seat_role=%q; expected occupier or delegate)", sessionURN, seatRole))
		return
	}

	// Full state now that we know we have a live session to project from.
	state := s.inspect.State()

	// Occupant resolution. Live-but-unoccupied returns an empty cone, not an
	// error: the session is valid but nobody is driving right now.
	occupant, occupied := operad.ResolveSessionOccupant(state, sessionURN)
	if !occupied {
		writeJSON(w, http.StatusOK, map[string]any{
			"t":        atT,
			"session":  string(sessionURN),
			"occupant": nil,
			"nodes":    []map[string]any{},
		})
		return
	}

	isAdmin := operad.CheckAdminCapability(state, occupant)

	// Build the cone: map owner_urn → list of firing hooks.
	type firing struct {
		urn           graph.URN
		predicateKind string
		predicate     any
	}
	cone := make(map[graph.URN][]firing)
	// Walk only the t_hook bucket via the by-type accessor. Production
	// states use the index; hand-built test states fall back to a scan.
	for _, hookURN := range state.NodesOfType("t_hook") {
		n, ok := state.Nodes[hookURN]
		if !ok {
			continue
		}
		predProp, hasPred := n.Properties["predicate"]
		if !hasPred || predProp.Value == nil {
			continue
		}
		if !reactive.EvaluateThookPredicate(predProp.Value, &state, atT) {
			continue
		}
		// owner_urn may arrive as string or graph.URN depending on how the
		// hook was constructed — use stringPropValue so either form is
		// accepted (PR #14 review — Gemini).
		ownerURN := stringPropValue(n.Properties["owner_urn"])
		if ownerURN == "" {
			continue
		}
		// Extract predicate kind for the operator view.
		kind := ""
		if m, ok := predProp.Value.(map[string]any); ok {
			if k, ok := m["kind"].(string); ok {
				kind = k
			}
		}
		cone[graph.URN(ownerURN)] = append(cone[graph.URN(ownerURN)], firing{
			urn:           n.URN,
			predicateKind: kind,
			predicate:     predProp.Value,
		})
	}

	// Emit the response, walking owners in a deterministic order so the
	// output is stable across calls (helps clients render + diff).
	ownerURNs := make([]graph.URN, 0, len(cone))
	for urn := range cone {
		ownerURNs = append(ownerURNs, urn)
	}
	sort.Slice(ownerURNs, func(i, j int) bool { return ownerURNs[i] < ownerURNs[j] })

	result := make([]map[string]any, 0, len(ownerURNs))
	for _, urn := range ownerURNs {
		owner, ok := state.Nodes[urn]
		if !ok {
			continue // dangling owner — the hook points at a node that isn't here
		}
		// Admin-scope filter: non-admin occupants don't see admin-scoped types.
		if !isAdmin {
			if _, admin := adminScopeTypes[owner.TypeID]; admin {
				continue
			}
		}

		// Sort the hooks deterministically by URN.
		fires := cone[urn]
		sort.Slice(fires, func(i, j int) bool { return fires[i].urn < fires[j].urn })

		hooks := make([]map[string]any, 0, len(fires))
		for _, f := range fires {
			hooks = append(hooks, map[string]any{
				"urn":            string(f.urn),
				"predicate_kind": f.predicateKind,
				"predicate":      f.predicate,
			})
		}

		// Shallow-copy properties so the response carries just the stored
		// values (Value field), matching the shape used by /state/nodes
		// consumers like the operator UI.
		props := make(map[string]any, len(owner.Properties))
		for k, v := range owner.Properties {
			props[k] = v.Value
		}

		result = append(result, map[string]any{
			"urn":          string(urn),
			"type_id":      string(owner.TypeID),
			"properties":   props,
			"open_t_hooks": hooks,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"t":        atT,
		"session":  string(sessionURN),
		"occupant": string(occupant),
		"nodes":    result,
	})
}

// stringPropValue returns a node property's value coerced to string via
// fmt.Sprint. Use this wherever a property might legitimately carry either
// `string`, `graph.URN`, or another string-like alias — the bare
// `value.(string)` assertion silently returns "" on graph.URN and that
// drops the value on the floor without any error path (PR #14 review —
// Gemini).
//
// If the property is absent, returns the empty string. Callers that need
// a hard absent/present distinction should inspect the map directly.
func stringPropValue(p graph.Property) string {
	if p.Value == nil {
		return ""
	}
	if s, ok := p.Value.(string); ok {
		return s
	}
	return fmt.Sprint(p.Value)
}
