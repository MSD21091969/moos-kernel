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
