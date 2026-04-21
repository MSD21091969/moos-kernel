package kernel

import (
	"fmt"
	"strings"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/operad"
)

// §M11 liveness gate and §M12 admin-capability gate for kernel rewrites.
//
// Doctrine:
//
//   - §M11 says a kernel accepts a rewrite only when at least one WF19-LINKed
//     session holds the envelope's actor as has-occupant. Absent such a
//     seat-holder, the rewrite is rejected.
//   - §M12 says admin-scope rewrites additionally require the actor (after
//     has-occupant resolution) to hold the superadmin role via WF02 governs.
//
// Evaluation surface:
//
//   - §M11 evaluates emitter-context: the session this envelope runs under.
//     That session must pre-exist. For ApplyProgram, §M11 runs in preflight
//     against the batch-initial state — emitter references cannot depend on
//     prior envelopes in the same batch (see the initial-state-check
//     doctrine note in kb/research/kernel/20260421-t171-m11-m12-implementation-plan.md §2.4).
//
//   - §M12 evaluates target-operation: what the envelope does. That operation
//     can depend on nodes created earlier in the same batch. For ApplyProgram,
//     §M12 runs per-envelope inside the working-state loop — a MUTATE on a
//     node ADDed in envelope N-1 sees that node's type when classifying at
//     envelope N. Closes the Gemini PR 31 security-HIGH where §M12 was
//     evaluated against initial state and ADD-then-MUTATE could bypass the
//     admin gate.

// checkLiveness runs both §M11 and §M12 against rt.state (no working state
// evolves). Used by Apply. The combined form is safe here because Apply
// handles a single envelope — the target either exists at call time or it
// doesn't, and ApplyProgram's mid-batch scenario cannot arise.
//
// Caller must hold rt.mu at least for read. Returns nil on pass; a
// fmt.Errorf naming the doctrine section on reject.
func (rt *Runtime) checkLiveness(env graph.Envelope) error {
	if err := rt.checkLivenessM11(env, rt.state); err != nil {
		return err
	}
	return rt.checkLivenessM12(env, rt.state)
}

// checkLivenessM11 is the §M11 emitter-context gate. Single-phase: the
// session URN the envelope runs under must exist in the passed state and
// must have the actor as has-occupant (or the actor must itself be an
// occupied session node).
//
// Parameter `state` is explicit so callers can choose whether to evaluate
// against rt.state (Apply, single-envelope) or some other snapshot. For
// ApplyProgram we always pass the batch-initial state because §M11 is an
// emitter-pre-existence rule — emitter references cannot depend on prior
// envelopes in the batch.
//
// Returns nil on pass; a fmt.Errorf for each resolver failure kind.
func (rt *Runtime) checkLivenessM11(env graph.Envelope, state graph.GraphState) error {
	if rt.registry == nil {
		return nil
	}

	// System-internal allowlist precedes all M11/M12 enforcement. Sweep,
	// kernel actors, and bootstrap-infrastructure ADDs are below the
	// governance line and bypass both gates.
	if operad.SystemInternalEnvelope(env) {
		return nil
	}

	res := operad.ResolveSessionForEnvelope(state, env)
	switch res.Kind {
	case operad.ResolveSessionExplicit, operad.ResolveSessionInferred, operad.ResolveSessionActorIsSession:
		return nil
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
	return nil
}

// checkLivenessM12 is the §M12 admin-capability gate. Classifies the
// envelope via the registry's AdminScopeRewrite method against the passed
// state — if classified admin-scope, the actor must hold WF02 superadmin
// capability. The state parameter matters: for ApplyProgram, the caller
// passes the working state AFTER earlier envelopes in the batch have been
// folded so a MUTATE on a mid-batch-ADDed node sees the node's type
// correctly.
//
// The §M11 allowlist (SystemInternalEnvelope) is re-checked here to keep
// checkLivenessM12 safe to call in isolation — if a future caller uses it
// without first running checkLivenessM11, kernel-actor and infrastructure
// envelopes still bypass correctly.
//
// Returns nil on pass; a fmt.Errorf naming §M12 on reject.
func (rt *Runtime) checkLivenessM12(env graph.Envelope, state graph.GraphState) error {
	if rt.registry == nil {
		return nil
	}
	if operad.SystemInternalEnvelope(env) {
		return nil
	}
	if !rt.registry.AdminScopeRewrite(env, state) {
		return nil
	}
	if !operad.CheckAdminCapability(state, env.Actor) {
		return fmt.Errorf("kernel(§M12): actor=%s lacks WF02 superadmin capability for admin-scope rewrite",
			env.Actor)
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
