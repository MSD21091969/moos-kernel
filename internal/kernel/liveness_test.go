package kernel

import (
	"strings"
	"testing"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
	"moos/kernel/internal/operad"
)

// livenessTestRegistry builds a minimal operad.Registry that's non-nil (so
// the liveness gate activates) and declares just enough type surface for
// the ADDs the liveness tests emit.
func livenessTestRegistry() *operad.Registry {
	reg := operad.EmptyRegistry()
	for _, typeID := range []graph.TypeID{"session", "user", "agent", "workstation", "kernel", "program"} {
		reg.NodeTypes[typeID] = operad.NodeTypeSpec{ID: typeID, Stratum: "S2"}
	}
	// No WF categories declared here — the liveness tests never LINK. State
	// (sessions, has-occupant relations) is injected directly via rt.state
	// map writes so we don't go through ValidateLINK / port-color checks.
	return reg
}

// newLivenessRuntime constructs a Runtime with the liveness-test registry and
// a memory store. State is pre-built by injectOccupancy rather than by
// going through SeedIfAbsent so tests avoid the LINK-validation surface
// entirely — they're exercising §M11 liveness, not port validation.
func newLivenessRuntime(t *testing.T) *Runtime {
	t.Helper()
	return &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    livenessTestRegistry(),
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}
}

// injectOccupancy mutates rt.state directly to seat `actorURN` on
// `sessionURN` via a canonical has-occupant relation. Bypasses the operad
// validators — these tests exercise §M11 liveness, not LINK validation,
// and the non-test path for installing occupancy is well-covered by the
// RotateSessionOccupant + PR-1 tests in the operad package.
func injectOccupancy(rt *Runtime, sessionURN, actorURN graph.URN, actorType graph.TypeID) {
	rt.state.Nodes[sessionURN] = graph.Node{URN: sessionURN, TypeID: "session"}
	rt.state.Nodes[actorURN] = graph.Node{URN: actorURN, TypeID: actorType}
	relURN := graph.URN("urn:moos:rel:" + string(sessionURN) + ".has-occupant." + string(actorURN))
	rt.state.Relations[relURN] = graph.Relation{
		URN: relURN, RewriteCategory: graph.WF19,
		SrcURN: sessionURN, SrcPort: "has-occupant",
		TgtURN: actorURN, TgtPort: "is-occupant-of",
	}
	// Maintain the indexes so ResolveSessionForEnvelope's RelationsFrom /
	// RelationsTo walks hit the fast path.
	if rt.state.RelationsBySrc[sessionURN] == nil {
		rt.state.RelationsBySrc[sessionURN] = map[graph.URN]struct{}{}
	}
	rt.state.RelationsBySrc[sessionURN][relURN] = struct{}{}
	if rt.state.RelationsByTgt[actorURN] == nil {
		rt.state.RelationsByTgt[actorURN] = map[graph.URN]struct{}{}
	}
	rt.state.RelationsByTgt[actorURN][relURN] = struct{}{}
}

// TestApply_M11_InferredPath_Accepted — single-session agent emits an
// envelope with no SessionURN. Reverse-lookup finds exactly one candidate;
// liveness passes.
func TestApply_M11_InferredPath_Accepted(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.solo",
		"urn:moos:agent:claude",
		"agent",
	)

	// Envelope with no SessionURN, user-space actor, non-infra type.
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude",
		NodeURN:     "urn:moos:program:x",
		TypeID:      "program",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("inferred liveness should accept; got %v", err)
	}
}

// TestApply_M11_ExplicitPath_Accepted — envelope sets SessionURN explicitly,
// and the session's has-occupant points at actor. Explicit path passes.
func TestApply_M11_ExplicitPath_Accepted(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude",
		SessionURN:  "urn:moos:session:sam.a",
		NodeURN:     "urn:moos:program:y",
		TypeID:      "program",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("explicit liveness should accept; got %v", err)
	}
}

// TestApply_M11_Ambiguous_Rejected — agent occupies two sessions and the
// envelope omits SessionURN; liveness must refuse to guess.
func TestApply_M11_Ambiguous_Rejected(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	// Second session with the same occupant.
	injectOccupancy(rt,
		"urn:moos:session:sam.b",
		"urn:moos:agent:claude",
		"agent",
	)

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude",
		NodeURN:     "urn:moos:program:z",
		TypeID:      "program",
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("ambiguous session should be rejected")
	}
	if !strings.Contains(err.Error(), "§M11") {
		t.Errorf("error should cite §M11; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "multiple sessions") {
		t.Errorf("error should mention multiple sessions; got %q", err.Error())
	}
}

// TestApply_M11_Absent_Rejected — actor has no has-occupant relation at
// all, and the envelope does not set SessionURN. Liveness fails closed.
func TestApply_M11_Absent_Rejected(t *testing.T) {
	rt := newLivenessRuntime(t)
	// Seed an unoccupied agent node directly.
	rt.state.Nodes["urn:moos:agent:orphan"] = graph.Node{
		URN: "urn:moos:agent:orphan", TypeID: "agent",
	}

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:orphan",
		NodeURN:     "urn:moos:program:q",
		TypeID:      "program",
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("absent session should be rejected")
	}
	if !strings.Contains(err.Error(), "§M11") {
		t.Errorf("error should cite §M11; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no session context") {
		t.Errorf("error should mention missing context; got %q", err.Error())
	}
}

// TestApply_M11_ExplicitMismatch_Rejected — envelope names a session but
// that session does not have-occupant -> actor. Liveness rejects.
func TestApply_M11_ExplicitMismatch_Rejected(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	// Seed another agent not occupying sam.a.
	if err := rt.SeedIfAbsent(graph.Envelope{
		RewriteType: graph.ADD, Actor: "urn:moos:user:sam",
		NodeURN: "urn:moos:agent:intruder", TypeID: "agent",
	}); err != nil {
		t.Fatalf("seed intruder: %v", err)
	}

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:intruder",
		SessionURN:  "urn:moos:session:sam.a", // claude occupies this, not intruder
		NodeURN:     "urn:moos:program:bad",
		TypeID:      "program",
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("explicit-mismatch should be rejected")
	}
	if !strings.Contains(err.Error(), "§M11") {
		t.Errorf("error should cite §M11; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "has-occupant") {
		t.Errorf("error should mention has-occupant; got %q", err.Error())
	}
}

// TestApply_M11_Allowlist_KernelActor — kernel-actor envelopes bypass §M11
// via operad.SystemInternalEnvelope. Essential for sweep + reactive paths
// and for admin tooling that emits kernel-authority rewrites.
func TestApply_M11_Allowlist_KernelActor(t *testing.T) {
	rt := newLivenessRuntime(t)
	// No sessions seeded — kernel actor should still pass liveness.
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:kernel:hp-z440.primary",
		NodeURN:     "urn:moos:program:internal",
		TypeID:      "program",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("kernel-actor envelope should bypass §M11; got %v", err)
	}
}

// TestApply_M11_Allowlist_InfrastructureADD — ADD of user/workstation/kernel
// bypasses §M11 even with a user actor, covering the bootstrap path where
// seeding happens before any session exists.
func TestApply_M11_Allowlist_InfrastructureADD(t *testing.T) {
	rt := newLivenessRuntime(t)
	cases := []graph.TypeID{"user", "workstation", "kernel"}
	for i, typeID := range cases {
		env := graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:user:sam",
			NodeURN:     graph.URN("urn:moos:" + string(typeID) + ":infra-" + string('a'+rune(i))),
			TypeID:      typeID,
		}
		if _, err := rt.Apply(env); err != nil {
			t.Errorf("infrastructure ADD type=%s should bypass §M11; got %v", typeID, err)
		}
	}
}

// TestSeedIfAbsent_BypassesLiveness — SeedIfAbsent applies envelopes with a
// user actor that has no session context. This would otherwise be rejected
// by §M11. The bypass is bootstrap-safe because seeds are idempotent and
// only ADD/LINK the infrastructure substrate.
func TestSeedIfAbsent_BypassesLiveness(t *testing.T) {
	rt := newLivenessRuntime(t)

	// Seed an ordinary non-infrastructure node via SeedIfAbsent. Without
	// the bypass, this would fail liveness (no session, non-infra type).
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:program:seed-only",
		TypeID:      "program",
	}
	if err := rt.SeedIfAbsent(env); err != nil {
		t.Fatalf("SeedIfAbsent should bypass liveness; got %v", err)
	}
}

// TestApplyProgram_M11_RejectionBailsAtomic — an all-or-nothing batch that
// includes one session-less envelope must reject the whole program without
// persisting any of its envelopes.
func TestApplyProgram_M11_RejectionBailsAtomic(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	preLen := rt.LogLen()

	// Two envelopes: first passes liveness via inferred path; second uses an
	// orphan actor and must fail. Whole batch is rejected.
	program := []graph.Envelope{
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:claude",
			NodeURN:     "urn:moos:program:good",
			TypeID:      "program",
		},
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:no-seat",
			NodeURN:     "urn:moos:program:bad",
			TypeID:      "program",
		},
	}
	if _, err := rt.ApplyProgram(program); err == nil {
		t.Fatalf("batch with one session-less envelope should reject")
	}
	if got := rt.LogLen(); got != preLen {
		t.Errorf("log advanced despite rejection: pre=%d post=%d", preLen, got)
	}
}

// TestReplay_PreservesPreM11Rewrites — fold.Replay does not re-run §M11
// because it does not re-validate. A persisted log produced before PR 3
// (no SessionURN field on any envelope) must rebuild state identically
// without error. This mirrors the PR 1 prospective-only invariant.
func TestReplay_PreservesPreM11Rewrites(t *testing.T) {
	// Synthetic log modeling pre-PR-3 envelopes: no SessionURN, user actor,
	// non-infrastructure ADDs. If fold.Replay were to call the liveness
	// gate, every one of these would fail.
	log := []graph.PersistedRewrite{
		{
			LogSeq: 1,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam",
				NodeURN:     "urn:moos:program:pre-m11-a",
				TypeID:      "program",
				Properties:  map[string]graph.Property{},
			},
		},
		{
			LogSeq: 2,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam",
				NodeURN:     "urn:moos:program:pre-m11-b",
				TypeID:      "program",
				Properties:  map[string]graph.Property{},
			},
		},
	}
	state, err := fold.Replay(log)
	if err != nil {
		t.Fatalf("replay should succeed regardless of §M11; got %v", err)
	}
	if _, ok := state.Nodes["urn:moos:program:pre-m11-a"]; !ok {
		t.Errorf("replayed state missing pre-m11-a")
	}
	if _, ok := state.Nodes["urn:moos:program:pre-m11-b"]; !ok {
		t.Errorf("replayed state missing pre-m11-b")
	}
}

// TestApplyProgram_M11_InitialStateCheck_RejectsIntraBatchSessionReference
// pins the design decision Guido flagged in the PR 30 review: the ApplyProgram
// preflight checks §M11 against the initial state (the state at the start of
// the batch), not against a working state that evolves envelope-by-envelope.
// A program that ADDs a session in envelope 1 and references that session in
// envelope 2's session_urn is REJECTED at preflight because the session does
// not yet exist when liveness checks the second envelope.
//
// This constraint is by design:
//
//   - Emitter context (the session the rewrite runs under) is what §M11
//     gates on — that session must pre-exist.
//   - Session birth + first occupant is a bootstrap pattern, handled by
//     SeedIfAbsent's structural skipLiveness bypass.
//   - Normal user-space atomic batches do NOT typically create a session and
//     emit from it in the same batch. If a future use case demands it, the
//     fix is to thread a working-state through the preflight loop (a design
//     change, not a bugfix).
func TestApplyProgram_M11_InitialStateCheck_RejectsIntraBatchSessionReference(t *testing.T) {
	rt := newLivenessRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.governance",
		"urn:moos:agent:claude",
		"agent",
	)
	preLen := rt.LogLen()

	program := []graph.Envelope{
		// Envelope 1: claude (occupying sam.governance) ADDs a brand-new
		// session node. Liveness passes because the emitter context is
		// sam.governance (exists, occupied).
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:claude",
			SessionURN:  "urn:moos:session:sam.governance",
			NodeURN:     "urn:moos:session:sam.newborn",
			TypeID:      "session",
		},
		// Envelope 2: tries to emit UNDER the just-created session via an
		// explicit session_urn pointing at it. This MUST fail preflight
		// because the initial-state check does not see sam.newborn yet.
		// If this ever starts passing, something changed in the preflight
		// discipline and §M11 semantics need a fresh look.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:claude",
			SessionURN:  "urn:moos:session:sam.newborn", // doesn't exist at batch start
			NodeURN:     "urn:moos:program:intra-batch",
			TypeID:      "program",
		},
	}
	_, err := rt.ApplyProgram(program)
	if err == nil {
		t.Fatalf("intra-batch session reference should be rejected at preflight")
	}
	if !strings.Contains(err.Error(), "§M11") {
		t.Errorf("error should cite §M11; got %q", err.Error())
	}
	if got := rt.LogLen(); got != preLen {
		t.Errorf("no envelopes should have persisted; pre=%d post=%d", preLen, got)
	}
}

// TestApply_M11_UnoccupiedSessionAsActor_Rejected pins the security fix
// from the Copilot finding on session_context.go:80 — a session-as-actor
// without a canonical has-occupant relation must be rejected, not passed
// through as ResolveSessionActorIsSession. Without this, an orphan
// session could emit user-space rewrites and bypass §M11 entirely.
func TestApply_M11_UnoccupiedSessionAsActor_Rejected(t *testing.T) {
	rt := newLivenessRuntime(t)
	// Session node exists but has no has-occupant.
	rt.state.Nodes["urn:moos:session:sam.orphan"] = graph.Node{
		URN: "urn:moos:session:sam.orphan", TypeID: "session",
	}

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:session:sam.orphan",
		NodeURN:     "urn:moos:program:sneak",
		TypeID:      "program",
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("unoccupied session-as-actor should be rejected")
	}
	if !strings.Contains(err.Error(), "§M11") {
		t.Errorf("error should cite §M11; got %q", err.Error())
	}
}

// TestApply_M11_OccupiedSessionAsActor_Accepted is the positive pair —
// a session that IS occupied (has-occupant points at a principal) can act
// as envelope actor. Common pattern for kernel-internal session-heartbeat
// and turn-count MUTATEs that carry session-as-actor.
func TestApply_M11_OccupiedSessionAsActor_Accepted(t *testing.T) {
	rt := newLivenessRuntime(t)
	// Seat an agent on the session so it's genuinely occupied.
	injectOccupancy(rt,
		"urn:moos:session:sam.seated",
		"urn:moos:agent:claude",
		"agent",
	)

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:session:sam.seated", // session-as-actor
		NodeURN:     "urn:moos:program:legal",
		TypeID:      "program",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("occupied session-as-actor should be accepted; got %v", err)
	}
}

// ------------------------------------------------------------------
// §M12 admin-capability gate — PR 4 integration tests
// ------------------------------------------------------------------

// adminLivenessRegistry extends the liveness-test registry with property
// specs the §M12 classifier consults (owner vs kernel authority_scope).
func adminLivenessRegistry() *operad.Registry {
	reg := livenessTestRegistry()
	reg.NodeTypes["program"] = operad.NodeTypeSpec{
		ID: "program", Stratum: "S2",
		Properties: map[string]operad.PropertySpec{
			"status":   {Mutability: "mutable", AuthorityScope: "owner"},
			"target_t": {Mutability: "mutable", AuthorityScope: "kernel"},
		},
	}
	// Ontology-governed types need declarations so ValidateADD doesn't reject
	// them as "unknown type_id" before §M12 runs.
	for _, t := range []graph.TypeID{"system_instruction", "gate", "twin_link", "transport_binding", "role"} {
		reg.NodeTypes[t] = operad.NodeTypeSpec{ID: t, Stratum: "S2"}
	}
	return reg
}

// injectSuperadminRole wires a role:superadmin node into state plus a
// WF02 governs LINK from principal → role. Mirrors the §M12 admin chain
// from operad.CheckAdminCapability.
func injectSuperadminRole(rt *Runtime, principal graph.URN) {
	const superadmin graph.URN = "urn:moos:role:superadmin"
	rt.state.Nodes[superadmin] = graph.Node{URN: superadmin, TypeID: "role"}

	relURN := graph.URN("urn:moos:rel:" + string(principal) + ".governs.superadmin")
	rt.state.Relations[relURN] = graph.Relation{
		URN: relURN, RewriteCategory: graph.WF02,
		SrcURN: principal, SrcPort: "governs",
		TgtURN: superadmin, TgtPort: "governed-by",
	}
	if rt.state.RelationsBySrc[principal] == nil {
		rt.state.RelationsBySrc[principal] = map[graph.URN]struct{}{}
	}
	rt.state.RelationsBySrc[principal][relURN] = struct{}{}
	if rt.state.RelationsByTgt[superadmin] == nil {
		rt.state.RelationsByTgt[superadmin] = map[graph.URN]struct{}{}
	}
	rt.state.RelationsByTgt[superadmin][relURN] = struct{}{}
}

func newAdminRuntime(t *testing.T) *Runtime {
	t.Helper()
	return &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    adminLivenessRegistry(),
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}
}

// Test 1 — Guido's list: non-admin actor + ontology-governed ADD is rejected.
func TestApply_M12_NonAdminActor_OntologyGovernedADD_Rejected(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	) // §M11 passes; §M12 is the gate under test

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude", // no superadmin
		NodeURN:     "urn:moos:gate:bad",
		TypeID:      "gate", // ontology-governed
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("ontology-governed ADD without superadmin should be rejected")
	}
	if !strings.Contains(err.Error(), "§M12") {
		t.Errorf("error should cite §M12; got %q", err.Error())
	}
}

// Test 2 — admin actor + ontology-governed ADD is accepted.
func TestApply_M12_AdminActor_OntologyGovernedADD_Accepted(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	injectSuperadminRole(rt, "urn:moos:agent:claude")

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude",
		NodeURN:     "urn:moos:system_instruction:persona.test",
		TypeID:      "system_instruction",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("admin actor should pass §M12; got %v", err)
	}
}

// Test 3 — ordinary type ADD passes §M12 even for non-admin actor.
func TestApply_M12_NonAdminActor_OrdinaryADD_NotAdminScope(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)

	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:agent:claude",
		NodeURN:     "urn:moos:program:ordinary",
		TypeID:      "program", // not ontology-governed
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("ordinary-type ADD should not trigger §M12; got %v", err)
	}
}

// Test 4 — non-admin actor MUTATEing a kernel-authority field on a
// non-kernel node is rejected with §M12.
func TestApply_M12_NonAdminActor_KernelAuthorityMUTATE_Rejected(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	// Pre-seed a program node with target_t (kernel-authority).
	rt.state.Nodes["urn:moos:program:p"] = graph.Node{
		URN: "urn:moos:program:p", TypeID: "program",
		Properties: map[string]graph.Property{
			"target_t": {Value: 190.0, Mutability: "mutable", AuthorityScope: "kernel"},
		},
	}

	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:agent:claude", // non-kernel, non-superadmin
		TargetURN:   "urn:moos:program:p",
		Field:       "target_t",
		NewValue:    200.0,
	}
	_, err := rt.Apply(env)
	if err == nil {
		t.Fatalf("kernel-authority MUTATE by non-admin non-kernel actor should be rejected")
	}
	if !strings.Contains(err.Error(), "§M12") {
		t.Errorf("error should cite §M12; got %q", err.Error())
	}
}

// Test 5 — owner-authority MUTATE is not admin-scope, passes.
func TestApply_M12_NonAdminActor_OwnerAuthorityMUTATE_NotAdminScope(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	rt.state.Nodes["urn:moos:program:p"] = graph.Node{
		URN: "urn:moos:program:p", TypeID: "program",
		Properties: map[string]graph.Property{
			"status":    {Value: "active", Mutability: "mutable", AuthorityScope: "owner"},
			"owner_urn": {Value: "urn:moos:agent:claude", Mutability: "immutable"},
		},
	}

	env := graph.Envelope{
		RewriteType:     graph.MUTATE,
		Actor:           "urn:moos:agent:claude",
		TargetURN:       "urn:moos:program:p",
		Field:           "status",
		NewValue:        "checkpoint",
		RewriteCategory: graph.WF18,
	}
	// Register WF18 so ValidateMUTATE's standard-path check doesn't trip
	// on "unknown rewrite_category". Minimal spec is fine — the §M12 test
	// doesn't exercise mutate_scope semantics.
	rt.registry.RewriteCategories[graph.WF18] = operad.RewriteCategorySpec{
		ID:              graph.WF18,
		AllowedRewrites: []graph.RewriteType{graph.MUTATE, graph.LINK, graph.ADD},
		MutateScope:     []string{"status"},
	}

	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("owner-authority MUTATE should pass §M12; got %v", err)
	}
}

// Test 6 — kernel-URN actor bypasses §M12 via §M11 allowlist (precedence).
func TestApply_M12_KernelActor_OntologyGovernedADD_AllowlistBeforeM12(t *testing.T) {
	rt := newAdminRuntime(t)
	// No sessions, no superadmin role. Kernel actor bypasses §M11 entirely
	// via SystemInternalEnvelope, and that bypass precedes the §M12 check
	// so admin-scope never runs for kernel actors.
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:kernel:hp-z440.primary",
		NodeURN:     "urn:moos:gate:internal",
		TypeID:      "gate",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("kernel-actor ontology-governed ADD should be allowlisted before §M12; got %v", err)
	}
}

// Test 7 — SeedIfAbsent bypass passes both §M11 and §M12.
func TestSeedIfAbsent_M12_OntologyGovernedADD_BypassesBoth(t *testing.T) {
	rt := newAdminRuntime(t)
	// No occupancy, no superadmin — seed should still land via skipLiveness.
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:transport_binding:bootstrap",
		TypeID:      "transport_binding",
	}
	if err := rt.SeedIfAbsent(env); err != nil {
		t.Fatalf("SeedIfAbsent should bypass §M12; got %v", err)
	}
}

// TestApplyProgram_M12_IntraBatchADDThenKernelAuthorityMUTATE_Rejected
// pins the Gemini security-HIGH fix on PR 31: a batch that ADDs a new
// program node and in the same batch MUTATEs a kernel-authority property
// on it previously bypassed §M12 because the preflight checker saw the
// target missing from the batch-initial state and classified as
// not-admin-scope. Post-fix, §M12 runs against the working state inside
// the write-locked loop; by envelope 2, the program node exists and its
// kernel-authority field is correctly classified admin-scope.
func TestApplyProgram_M12_IntraBatchADDThenKernelAuthorityMUTATE_Rejected(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	) // claude occupies a session, passes §M11, no superadmin

	preLen := rt.LogLen()

	program := []graph.Envelope{
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:claude",
			NodeURN:     "urn:moos:program:freshly-baked",
			TypeID:      "program",
		},
		{
			RewriteType: graph.MUTATE,
			Actor:       "urn:moos:agent:claude",
			TargetURN:   "urn:moos:program:freshly-baked", // created in envelope 1
			Field:       "target_t",                        // authority_scope=kernel per type spec
			NewValue:    200.0,
		},
	}
	_, err := rt.ApplyProgram(program)
	if err == nil {
		t.Fatalf("ADD+kernel-authority-MUTATE batch without superadmin must be rejected")
	}
	if !strings.Contains(err.Error(), "§M12") {
		t.Errorf("error should cite §M12; got %q", err.Error())
	}
	if got := rt.LogLen(); got != preLen {
		t.Errorf("atomic rejection should leave log unchanged; pre=%d post=%d", preLen, got)
	}
}

// TestApplyProgram_M12_IntraBatchADDThenOwnerMUTATE_Passes confirms the
// fix does not over-reject: a batch that ADDs a program and then
// MUTATEs an owner-authority field on it (e.g. status) still passes §M12
// because the mid-batch classification correctly sees the target as a
// program (not ontology-governed) and the field as owner-authority.
func TestApplyProgram_M12_IntraBatchADDThenOwnerMUTATE_Passes(t *testing.T) {
	rt := newAdminRuntime(t)
	injectOccupancy(rt,
		"urn:moos:session:sam.a",
		"urn:moos:agent:claude",
		"agent",
	)
	// Register WF18 so ValidateMUTATE is happy.
	rt.registry.RewriteCategories[graph.WF18] = operad.RewriteCategorySpec{
		ID:              graph.WF18,
		AllowedRewrites: []graph.RewriteType{graph.ADD, graph.MUTATE, graph.LINK},
		MutateScope:     []string{"status"},
	}

	program := []graph.Envelope{
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:agent:claude",
			NodeURN:     "urn:moos:program:ordinary",
			TypeID:      "program",
			Properties: map[string]graph.Property{
				"status":    {Value: "active", Mutability: "mutable", AuthorityScope: "owner"},
				"owner_urn": {Value: "urn:moos:agent:claude", Mutability: "immutable"},
			},
		},
		{
			RewriteType:     graph.MUTATE,
			Actor:           "urn:moos:agent:claude",
			TargetURN:       "urn:moos:program:ordinary",
			Field:           "status", // owner-authority
			NewValue:        "checkpoint",
			RewriteCategory: graph.WF18,
		},
	}
	if _, err := rt.ApplyProgram(program); err != nil {
		t.Fatalf("owner-authority MUTATE on same-batch-ADDed program must pass §M12; got %v", err)
	}
}

// Test 8 — fold.Replay of pre-PR-4 rewrites that would now be admin-scope
// still replays cleanly. fold doesn't call checkLiveness, so §M12 has no
// retroactive effect. Mirrors the prospective-only invariant for §M11
// and the port-pair validator from PR 1.
func TestReplay_M12_PreservesPrePR4AdminScopeRewrites(t *testing.T) {
	log := []graph.PersistedRewrite{
		{
			LogSeq: 1,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam", // non-admin
				NodeURN:     "urn:moos:gate:pre-pr4",
				TypeID:      "gate",
				Properties: map[string]graph.Property{
					"predicate_expr": {Value: "true", Mutability: "immutable"},
				},
			},
		},
		{
			LogSeq: 2,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam",
				NodeURN:     "urn:moos:system_instruction:pre-pr4",
				TypeID:      "system_instruction",
				Properties: map[string]graph.Property{
					"content": {Value: "legacy", Mutability: "mutable"},
				},
			},
		},
	}
	state, err := fold.Replay(log)
	if err != nil {
		t.Fatalf("replay of pre-PR-4 admin-scope rewrites should succeed; got %v", err)
	}
	if _, ok := state.Nodes["urn:moos:gate:pre-pr4"]; !ok {
		t.Errorf("replayed state missing gate:pre-pr4")
	}
	if _, ok := state.Nodes["urn:moos:system_instruction:pre-pr4"]; !ok {
		t.Errorf("replayed state missing system_instruction:pre-pr4")
	}
}

// TestApply_RegistryLess_LivenessNoop — when the Runtime has no registry
// loaded (--ontology omitted), the liveness gate is a no-op. Preserves the
// existing "registry-less mode" UX and matches the pattern used by the
// operad validators (which also short-circuit on nil registry).
func TestApply_RegistryLess_LivenessNoop(t *testing.T) {
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil, // the defining condition
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:program:nofilter",
		TypeID:      "program",
	}
	if _, err := rt.Apply(env); err != nil {
		t.Fatalf("registry-less mode should not gate on §M11; got %v", err)
	}
}
