package operad

import "moos/kernel/internal/graph"

// Session-occupancy and admin-capability helpers (§M11 / §M12 / §M19).
//
// These are PURE functions over graph state — no locking, no IO. They are
// exported so the kernel runtime (Apply gate path), transport handlers
// (t-cone renderer), and tests can all share one implementation of the
// walk from a session actor through the has-occupant relation to the
// principal and onward to the superadmin role via WF02 governance.
//
// §M19 relation shape (v3.10 ontology):
//
//	session --has-occupant (ColorWorkflow)--> user | agent
//	        <--is-occupant-of--
//
// §M12 admin chain:
//
//	actor (session OR user OR agent)
//	  -- (if session) has-occupant --> principal
//	  -- WF02 governs --> role:superadmin
//
// Superadmin role is identified strictly by URN equality against
// urn:moos:role:superadmin. Future work may extend to a capability-scope
// check, but the binary superadmin gate is what §M12 declares as the
// admin-authority predicate for v3.10.

// superadminRoleURN is the canonical superadmin role node URN. The kernel
// seeds this node (or an admin creates it via governance_proposal); the
// admin-capability check walks WF02 governs relations into it.
const superadminRoleURN graph.URN = "urn:moos:role:superadmin"

// ResolveSessionOccupant walks the WF19 has-occupant relation outbound from
// a session node and returns the principal URN (user or agent) it points at.
//
// Returns (urn, true) on success; (zero, false) if:
//   - the session node doesn't exist
//   - the session has no has-occupant relation (unoccupied)
//   - the target of has-occupant is missing from state
//
// Pure function — no locking. Caller passes a state snapshot.
//
// v3.10-aware: the walk matches on SrcPort=="has-occupant" so it works
// against any WF (WF19 canonical; future WFs that adopt the same port
// name would also compose). Multiple has-occupant relations for one
// session is a doctrine violation — v3.10 says "exactly one at a time",
// but we return the first one we find rather than panicking; callers
// that need stricter semantics can fold over the full state.
func ResolveSessionOccupant(state graph.GraphState, sessionURN graph.URN) (graph.URN, bool) {
	if sessionURN == "" {
		return "", false
	}
	if _, ok := state.Nodes[sessionURN]; !ok {
		return "", false
	}
	for _, rel := range state.Relations {
		if rel.SrcURN != sessionURN {
			continue
		}
		if rel.SrcPort != "has-occupant" {
			continue
		}
		if _, ok := state.Nodes[rel.TgtURN]; !ok {
			continue
		}
		return rel.TgtURN, true
	}
	return "", false
}

// CheckAdminCapability returns true iff the actor URN resolves (possibly
// through a has-occupant hop, if actor is a session) to a principal that
// holds the superadmin role via a WF02 governs relation.
//
// The admin chain (§M12):
//
//	actor
//	  -- if type_id=="session": has-occupant --> principal
//	  -- WF02 governs --> urn:moos:role:superadmin
//
// Fail-closed on any missing hop. An empty actor URN, unknown actor node,
// unoccupied session, or absent superadmin role relation all return false.
//
// Pure function — no locking, no IO.
//
// Intended call site is the kernel Apply gate path for ontology-scope
// rewrites: a rewrite that touches admin-governed fields (e.g. ontology
// migrations, grammar_fragment status transitions, session.seat_role
// changes) should gate on CheckAdminCapability(state, env.Actor).
//
// See v3.10 ontology changelog + round-9 plan §M4 for context.
func CheckAdminCapability(state graph.GraphState, actor graph.URN) bool {
	if actor == "" {
		return false
	}
	actorNode, ok := state.Nodes[actor]
	if !ok {
		return false
	}

	// If actor is a session, hop through has-occupant to the principal.
	principal := actor
	if actorNode.TypeID == "session" {
		occ, ok := ResolveSessionOccupant(state, actor)
		if !ok {
			return false // unoccupied session → no one to check
		}
		principal = occ
	}

	// Walk WF02 governs outbound from the principal; look for the
	// superadmin role URN as the target.
	for _, rel := range state.Relations {
		if rel.SrcURN != principal {
			continue
		}
		if rel.SrcPort != "governs" {
			continue
		}
		if rel.TgtURN == superadminRoleURN {
			// Verify the target node actually exists in state — otherwise
			// the LINK is stale and we should fail closed.
			if _, ok := state.Nodes[superadminRoleURN]; !ok {
				return false
			}
			return true
		}
	}
	return false
}
