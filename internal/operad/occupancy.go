package operad

import (
	"fmt"

	"moos/kernel/internal/graph"
)

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

// hasOccupantSrcPort / isOccupantOfTgtPort are the canonical v3.10 WF19
// port pair for §M19 session-occupancy. Kept as named constants so helpers
// (ResolveSessionOccupant, RotateSessionOccupant) and tests all spell them
// the same way.
const (
	hasOccupantSrcPort   = "has-occupant"
	isOccupantOfTgtPort  = "is-occupant-of"
)

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
		if rel.SrcPort != hasOccupantSrcPort || rel.TgtPort != isOccupantOfTgtPort {
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

// RotateOccupantResult is the output of RotateSessionOccupant: the atomic
// program to submit (one or two envelopes), plus metadata useful for logging
// and test assertions.
type RotateOccupantResult struct {
	// Envelopes is the atomic program: an UNLINK of the prior has-occupant
	// relation followed by a LINK of the new one. When the session is
	// unoccupied on entry, Envelopes contains only the LINK envelope and
	// PriorRelationURN is empty. The caller submits the slice via
	// runtime.ApplyProgram — atomicity is the caller's responsibility (the
	// helper is pure and does not touch state).
	Envelopes []graph.Envelope

	// PriorRelationURN is the URN of the has-occupant relation that was
	// UNLINKed, or "" if the session was unoccupied on entry.
	PriorRelationURN graph.URN

	// PriorOccupant is the URN of the occupant that was rotated out, or ""
	// if the session was unoccupied on entry.
	PriorOccupant graph.URN
}

// RotateSessionOccupant builds an atomic program that rotates the §M19
// has-occupant relation on sessionURN from its current occupant (if any) to
// newOccupantURN. The helper is pure — it only reads state and emits
// envelopes. The caller is responsible for submitting the returned program
// via runtime.ApplyProgram so UNLINK+LINK land atomically.
//
// DOCTRINE NOTE — rotation as two envelopes, not one:
//
// The v3.12 ontology description of the has-occupant additional_port_pair
// declares "Rotation = MUTATE of the LINK's target_urn (atomic; preserves
// session identity per CI-3)". MUTATE envelopes in the current kernel model
// target nodes only (env.TargetURN is a node URN; relations carry no
// mutable properties). Rather than introduce a fifth rewrite operation or
// overload MUTATE with a relation path, rotation is realized as an atomic
// UNLINK + LINK program on the same (SrcURN, WF19 port pair) — which
// preserves the invariant the ontology description intends:
//
//   - Session identity is unchanged (sessionURN is fixed across the program).
//   - At the atomic boundary, has-occupant points at exactly one target
//     (either the prior one pre-commit or newOccupantURN post-commit; no
//     intermediate state where both or neither is visible to readers).
//   - §M19 "at-most-one has-occupant per session" (D22.2) is preserved.
//
// If Guido's review of the ontology description prefers a literal "MUTATE
// target_urn on a LINK" operation, a follow-up PR can add that as a fifth
// rewrite_type or extend MUTATE with a RelationURN discriminator. For now
// rotation is a helper-emitted 2-envelope program.
//
// Failure modes (all return a non-nil error; Envelopes is nil):
//   - sessionURN empty
//   - newOccupantURN empty
//   - newRelationURN empty
//   - actor empty (fold.validateEnvelopeStructure would reject the emitted
//     envelopes with ErrMissingActor; fail early here for a clearer error)
//   - session node missing from state OR not of type_id == "session"
//   - newOccupantURN not of a principal type (user | agent)
//   - current occupant equals newOccupantURN (no-op rotation rejected to
//     surface programmer bugs; callers who want idempotence check first)
//   - session has MORE THAN ONE has-occupant relation (doctrine violation;
//     rotation requires a pre-rotation cleanup pass)
//   - newRelationURN already exists in state (relation-URN reuse; would
//     LINK-fail with ErrRelationExists at apply time)
//
// On success with an unoccupied session, the program is a single LINK
// envelope — an "initial seat" rather than a rotation in the strict sense,
// but the helper covers this case because callers at session-birth time
// should not need to dispatch to a different helper.
//
// newRelationURN is the URN for the NEW has-occupant relation. Callers
// generate it with whatever convention suits them (a conventional shape is
// urn:moos:rel:<session-short>.has-occupant.<occupant-short>). It must not
// collide with any existing relation in state — not just PriorRelationURN —
// to avoid ErrRelationExists at apply time.
func RotateSessionOccupant(
	state graph.GraphState,
	sessionURN graph.URN,
	newOccupantURN graph.URN,
	actor graph.URN,
	newRelationURN graph.URN,
) (RotateOccupantResult, error) {
	var zero RotateOccupantResult

	if sessionURN == "" {
		return zero, rotateErr("sessionURN is empty")
	}
	if newOccupantURN == "" {
		return zero, rotateErr("newOccupantURN is empty")
	}
	if newRelationURN == "" {
		return zero, rotateErr("newRelationURN is empty")
	}
	// actor must be non-empty: fold.validateEnvelopeStructure rejects empty
	// Actor with ErrMissingActor, so a helper that let empty actor through
	// would build a "successful" program that deterministically fails at
	// submission time. Fail here for a clearer error path.
	if actor == "" {
		return zero, rotateErr("actor is empty")
	}

	sessionNode, ok := state.Nodes[sessionURN]
	if !ok {
		return zero, rotateErr("session node not found: %s", sessionURN)
	}
	if sessionNode.TypeID != "session" {
		return zero, rotateErr("urn is not a session: %s (type_id=%s)", sessionURN, sessionNode.TypeID)
	}

	newOccupantNode, ok := state.Nodes[newOccupantURN]
	if !ok {
		return zero, rotateErr("new occupant node not found: %s", newOccupantURN)
	}
	if _, isPrincipal := principalTypes[newOccupantNode.TypeID]; !isPrincipal {
		return zero, rotateErr("new occupant %s has type_id=%s, must be user|agent",
			newOccupantURN, newOccupantNode.TypeID)
	}

	// Gather all has-occupant relations outbound from the session. More than
	// one is a doctrine violation (§M19 at-most-one); zero is unoccupied.
	// Early-break on the second match — we only need to distinguish 0 / 1 / >1.
	var priorRelURN graph.URN
	var priorOccupant graph.URN
	count := 0
	for _, relURN := range state.RelationsFrom(sessionURN) {
		rel, ok := state.Relations[relURN]
		if !ok {
			continue
		}
		if rel.SrcPort != hasOccupantSrcPort || rel.TgtPort != isOccupantOfTgtPort {
			continue
		}
		count++
		if count > 1 {
			break
		}
		priorRelURN = rel.URN
		priorOccupant = rel.TgtURN
	}
	if count > 1 {
		return zero, rotateErr("session %s has %d has-occupant relations (§M19 at-most-one violation); cleanup required before rotation",
			sessionURN, count)
	}

	// No-op rotation: same occupant as current. Surface as an error so the
	// caller can decide whether to treat it as a bug or a benign skip; the
	// helper does not silently succeed.
	if count == 1 && priorOccupant == newOccupantURN {
		return zero, rotateErr("session %s already occupied by %s (rotation is a no-op)",
			sessionURN, newOccupantURN)
	}

	// Stronger than priorRelURN comparison: reject if newRelationURN matches
	// ANY existing relation. Catches URN reuse across unrelated relations
	// (which would cause ErrRelationExists at LINK apply time with a less
	// informative message) in addition to the prior-has-occupant case.
	if _, exists := state.Relations[newRelationURN]; exists {
		return zero, rotateErr("newRelationURN %s already exists in state; pick a distinct URN for the new LINK",
			newRelationURN)
	}

	linkEnv := graph.Envelope{
		RewriteType:     graph.LINK,
		Actor:           actor,
		RelationURN:     newRelationURN,
		SrcURN:          sessionURN,
		SrcPort:         hasOccupantSrcPort,
		TgtURN:          newOccupantURN,
		TgtPort:         isOccupantOfTgtPort,
		RewriteCategory: graph.WF19,
	}

	if count == 0 {
		// Unoccupied session — initial seat. Single-envelope program.
		return RotateOccupantResult{
			Envelopes: []graph.Envelope{linkEnv},
		}, nil
	}

	unlinkEnv := graph.Envelope{
		RewriteType:     graph.UNLINK,
		Actor:           actor,
		RelationURN:     priorRelURN,
		RewriteCategory: graph.WF19,
	}
	return RotateOccupantResult{
		Envelopes:        []graph.Envelope{unlinkEnv, linkEnv},
		PriorRelationURN: priorRelURN,
		PriorOccupant:    priorOccupant,
	}, nil
}

// rotateErr wraps a rotation-failure message with an "operad:" prefix so it
// reads uniformly with other operad validation errors in logs.
func rotateErr(format string, args ...any) error {
	return fmt.Errorf("operad: rotate_session_occupant: "+format, args...)
}
