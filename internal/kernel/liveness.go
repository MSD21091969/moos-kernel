package kernel

import (
	"fmt"
	"strings"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/operad"
)

// §M11 liveness gate and §M12 admin-capability hook for kernel rewrites.
//
// Doctrine:
//
//   - §M11 says a kernel accepts a rewrite only when at least one WF19-LINKed
//     session holds the envelope's actor as has-occupant. Absent such a
//     seat-holder, the rewrite is rejected.
//   - §M12 says admin-scope rewrites additionally require the actor (after
//     has-occupant resolution) to hold the superadmin role via WF02 governs.
//
// This file integrates both checks into Runtime.Apply / Runtime.ApplyProgram
// as small helpers so the main Apply body stays readable. §M12 is plumbed
// here via operad.AdminScopeRewrite + operad.CheckAdminCapability and ships
// as dormant in PR 3 (AdminScopeRewrite returns false until PR 4 fills it in).

// checkLiveness is the §M11 gate. Called from Apply / ApplyProgram BEFORE
// operad validation so we fail fast on missing session context without
// paying structural-validation cost. Read-only over state; no locking
// required because Apply/ApplyProgram are already serialised by rt.mu in
// the caller (or read-lock during validation).
//
// Order of checks:
//  1. System-internal allowlist (sweep, kernel-actor, infrastructure ADD).
//     These are below the governance line and always pass.
//  2. Session-context resolution via operad.ResolveSessionForEnvelope.
//     Explicit env.SessionURN wins; inferred from Actor when unambiguous;
//     reject when ambiguous or absent.
//  3. (PR 4 hook) admin-scope classification + capability check. Dormant
//     in PR 3.
//
// Returns nil on pass, a fmt.Errorf wrapping the failure mode on reject.
// Error messages name the doctrine section so log readers can trace back.
func (rt *Runtime) checkLiveness(env graph.Envelope) error {
	// Registry-less mode (no --ontology): liveness is a no-op. The kernel
	// still replays and applies rewrites; it just does not enforce the
	// occupancy invariant. Matches the existing pattern where validators
	// short-circuit when registry is nil.
	if rt.registry == nil {
		return nil
	}

	// Step 1 — system-internal allowlist. Sweep, kernel actors, and
	// bootstrap-infrastructure ADDs are below the liveness line.
	if operad.SystemInternalEnvelope(env) {
		return nil
	}

	// Step 2 — resolve session context. Pass the live state so reverse
	// has-occupant walks see the most recent seat assignments.
	res := operad.ResolveSessionForEnvelope(rt.state, env)
	switch res.Kind {
	case operad.ResolveSessionExplicit, operad.ResolveSessionInferred, operad.ResolveSessionActorIsSession:
		// Pass: a single, verified session context was resolved.
	case operad.ResolveSessionExplicitMismatch:
		return fmt.Errorf("kernel(§M11): envelope names session_urn=%s but that session is missing, not of type session, or does not have has-occupant -> actor=%s",
			res.SessionURN, env.Actor)
	case operad.ResolveSessionAmbiguous:
		return fmt.Errorf("kernel(§M11): actor=%s occupies multiple sessions (%s); envelope must set session_urn to disambiguate",
			env.Actor, renderURNList(res.Candidates))
	case operad.ResolveSessionAbsent:
		return fmt.Errorf("kernel(§M11): no session context for actor=%s (no has-occupant relation in state and no session_urn on envelope)",
			env.Actor)
	}

	// Step 3 — admin-scope gate (§M12 hook). Dormant in PR 3 — the
	// classifier currently returns false for every envelope. PR 4 tightens
	// this to check capability via operad.CheckAdminCapability.
	if operad.AdminScopeRewrite(env, rt.state) {
		if !operad.CheckAdminCapability(rt.state, env.Actor) {
			return fmt.Errorf("kernel(§M12): actor=%s lacks WF02 superadmin capability for admin-scope rewrite",
				env.Actor)
		}
	}

	return nil
}

// renderURNList formats a URN slice for inclusion in error messages.
// Empty list renders as "[]"; non-empty as "[u1, u2, ...]".
func renderURNList(urns []graph.URN) string {
	if len(urns) == 0 {
		return "[]"
	}
	parts := make([]string, len(urns))
	for i, u := range urns {
		parts[i] = string(u)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
