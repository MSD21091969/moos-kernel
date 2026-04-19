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
//	  -- (if session) has-occupant --> principal (user | agent)
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

// principalTypes enumerates the node type_ids that may act as a principal
// in the §M12 chain. Any other type_id on the actor (or on the resolved
// occupant) fails the admin check closed.
var principalTypes = map[graph.TypeID]struct{}{
	"user":  {},
	"agent": {},
}

// ResolveSessionOccupant walks the WF19 has-occupant relation outbound from
// a session node and returns the principal URN (user or agent) it points at.
//
// Returns (urn, true) on success; (zero, false) if:
//   - the session URN is empty or the node is missing
//   - the node exists but is not a type_id=="session"
//   - the session has no well-formed has-occupant relation (unoccupied)
//   - the target of has-occupant is missing from state
//   - the target's type_id is not a recognised principal (user | agent)
//
// Pure function — no locking. Caller passes a state snapshot.
//
// v3.10-aware: the walk requires BOTH SrcPort=="has-occupant" AND
// TgtPort=="is-occupant-of" to match, which tightens against accidental
// matches where another relation happened to reuse one of the port names.
// Multiple has-occupant relations for a single session is a doctrine
// violation; we return the first one found in the state's relation map
// (Go map iteration order is randomized, so this is not stable under
// conflict — callers that care about the doctrine violation should
// detect duplicates explicitly).
func ResolveSessionOccupant(state graph.GraphState, sessionURN graph.URN) (graph.URN, bool) {
	if sessionURN == "" {
		return "", false
	}
	sessionNode, ok := state.Nodes[sessionURN]
	if !ok {
		return "", false
	}
	// Tighten: the helper is documented as session-specific. If the URN
	// happens to match a non-session node, fail closed rather than silently
	// resolving against whatever has-occupant relation points out of it.
	if sessionNode.TypeID != "session" {
		return "", false
	}

	// Walk only the relations outbound from this session (O(edges-at-session),
	// typically 1-3) rather than scanning every relation in the graph.
	for _, relURN := range state.RelationsFrom(sessionURN) {
		rel, ok := state.Relations[relURN]
		if !ok {
			continue // index drift; skip
		}
		// Match on BOTH sides of the WF19 port pair to avoid false positives
		// from any future WF that reuses one of the port names.
		if rel.SrcPort != "has-occupant" || rel.TgtPort != "is-occupant-of" {
			continue
		}
		tgt, ok := state.Nodes[rel.TgtURN]
		if !ok {
			continue // stale LINK; skip
		}
		// Tighten: only user / agent are valid principals per WF19 v3.10.
		if _, isPrincipal := principalTypes[tgt.TypeID]; !isPrincipal {
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
// unoccupied session, principal of an unrecognised type, missing
// superadmin role, or absent WF02 relation all return false.
//
// The WF02 match is strict on both sides: RewriteCategory == WF02,
// SrcPort == "governs", TgtPort == "governed-by", TgtURN == superadmin.
// Relations that match only a subset (e.g. a stale link or a port
// collision) are ignored.
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

	// Hoist the superadmin existence check out of the relation loop —
	// if the role node itself is missing, no WF02 link can grant admin
	// authority (PR #13 review — Gemini).
	if _, ok := state.Nodes[superadminRoleURN]; !ok {
		return false
	}

	// Resolve the principal. If actor is a session, hop through has-occupant;
	// otherwise the actor IS the principal and must be user|agent.
	principal := actor
	principalNode := actorNode
	if actorNode.TypeID == "session" {
		occ, ok := ResolveSessionOccupant(state, actor)
		if !ok {
			return false // unoccupied session → no one to check
		}
		principal = occ
		principalNode = state.Nodes[principal]
	}
	if _, isPrincipal := principalTypes[principalNode.TypeID]; !isPrincipal {
		return false
	}

	// Walk only relations outbound from the principal (typically a
	// handful of WF02 governance links) instead of the entire relation
	// map. Strict WF02 match: rewrite_category + src/tgt ports + target URN.
	for _, relURN := range state.RelationsFrom(principal) {
		rel, ok := state.Relations[relURN]
		if !ok {
			continue // index drift; skip
		}
		if rel.RewriteCategory != graph.WF02 {
			continue
		}
		if rel.SrcPort != "governs" || rel.TgtPort != "governed-by" {
			continue
		}
		if rel.TgtURN == superadminRoleURN {
			return true
		}
	}
	return false
}
