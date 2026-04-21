package operad

import (
	"sort"
	"testing"

	"moos/kernel/internal/graph"
)

// sessionContextFixture builds a state with:
//   session:sam.a --has-occupant--> agent:claude
//   session:sam.b --has-occupant--> agent:claude   (only present in multi-seat variant)
//   session:sam.c (no occupant)
//   user:sam, agent:other (stand-alone)
func sessionContextFixture(multiSeat bool) graph.GraphState {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:session:sam.a": {URN: "urn:moos:session:sam.a", TypeID: "session"},
			"urn:moos:session:sam.b": {URN: "urn:moos:session:sam.b", TypeID: "session"},
			"urn:moos:session:sam.c": {URN: "urn:moos:session:sam.c", TypeID: "session"},
			"urn:moos:agent:claude":  {URN: "urn:moos:agent:claude", TypeID: "agent"},
			"urn:moos:agent:other":   {URN: "urn:moos:agent:other", TypeID: "agent"},
			"urn:moos:user:sam":      {URN: "urn:moos:user:sam", TypeID: "user"},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:a.occupant": {
				URN: "urn:moos:rel:a.occupant", RewriteCategory: graph.WF19,
				SrcURN: "urn:moos:session:sam.a", SrcPort: "has-occupant",
				TgtURN: "urn:moos:agent:claude", TgtPort: "is-occupant-of",
			},
		},
	}
	if multiSeat {
		state.Relations["urn:moos:rel:b.occupant"] = graph.Relation{
			URN: "urn:moos:rel:b.occupant", RewriteCategory: graph.WF19,
			SrcURN: "urn:moos:session:sam.b", SrcPort: "has-occupant",
			TgtURN: "urn:moos:agent:claude", TgtPort: "is-occupant-of",
		}
	}
	return state
}

func TestResolveSessionForEnvelope_ActorIsSession(t *testing.T) {
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor: "urn:moos:session:sam.a",
	})
	if res.Kind != ResolveSessionActorIsSession {
		t.Fatalf("kind = %v, want ActorIsSession", res.Kind)
	}
	if res.SessionURN != "urn:moos:session:sam.a" {
		t.Errorf("SessionURN = %s, want the actor URN", res.SessionURN)
	}
}

// TestResolveSessionForEnvelope_ActorIsSession_Unoccupied pins the fix for
// the Copilot finding on session_context.go:80 — a session-as-actor must
// still be occupied to satisfy §M11. An orphan session (no has-occupant)
// acting as its own envelope actor previously returned
// ResolveSessionActorIsSession and bypassed liveness entirely. Now it
// returns ResolveSessionAbsent and the liveness gate rejects.
func TestResolveSessionForEnvelope_ActorIsSession_Unoccupied(t *testing.T) {
	state := sessionContextFixture(false)
	// session:sam.c is a session node with no has-occupant in the fixture.
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor: "urn:moos:session:sam.c",
	})
	if res.Kind != ResolveSessionAbsent {
		t.Fatalf("kind = %v, want Absent for unoccupied session-as-actor", res.Kind)
	}
}

func TestResolveSessionForEnvelope_ExplicitOK(t *testing.T) {
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor:      "urn:moos:agent:claude",
		SessionURN: "urn:moos:session:sam.a",
	})
	if res.Kind != ResolveSessionExplicit {
		t.Fatalf("kind = %v, want Explicit", res.Kind)
	}
	if res.SessionURN != "urn:moos:session:sam.a" {
		t.Errorf("SessionURN echoes the explicit choice; got %s", res.SessionURN)
	}
}

func TestResolveSessionForEnvelope_ExplicitMismatch_WrongSession(t *testing.T) {
	// session:sam.c exists but has no has-occupant — naming it with actor
	// agent:claude should mismatch.
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor:      "urn:moos:agent:claude",
		SessionURN: "urn:moos:session:sam.c",
	})
	if res.Kind != ResolveSessionExplicitMismatch {
		t.Fatalf("kind = %v, want ExplicitMismatch", res.Kind)
	}
}

func TestResolveSessionForEnvelope_ExplicitMismatch_UnknownSession(t *testing.T) {
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor:      "urn:moos:agent:claude",
		SessionURN: "urn:moos:session:does-not-exist",
	})
	if res.Kind != ResolveSessionExplicitMismatch {
		t.Fatalf("kind = %v, want ExplicitMismatch for unknown session", res.Kind)
	}
}

func TestResolveSessionForEnvelope_ExplicitMismatch_NotASession(t *testing.T) {
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor:      "urn:moos:agent:claude",
		SessionURN: "urn:moos:user:sam", // exists but not a session
	})
	if res.Kind != ResolveSessionExplicitMismatch {
		t.Fatalf("kind = %v, want ExplicitMismatch for non-session URN", res.Kind)
	}
}

func TestResolveSessionForEnvelope_InferredSingleSession(t *testing.T) {
	state := sessionContextFixture(false)
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor: "urn:moos:agent:claude",
	})
	if res.Kind != ResolveSessionInferred {
		t.Fatalf("kind = %v, want Inferred", res.Kind)
	}
	if res.SessionURN != "urn:moos:session:sam.a" {
		t.Errorf("SessionURN = %s, want sam.a", res.SessionURN)
	}
}

func TestResolveSessionForEnvelope_AmbiguousMultipleSessions(t *testing.T) {
	state := sessionContextFixture(true) // agent:claude in both sam.a and sam.b
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor: "urn:moos:agent:claude",
	})
	if res.Kind != ResolveSessionAmbiguous {
		t.Fatalf("kind = %v, want Ambiguous", res.Kind)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(res.Candidates))
	}
	// Sort for deterministic assertion (map iteration order is randomized).
	got := make([]string, len(res.Candidates))
	for i, c := range res.Candidates {
		got[i] = string(c)
	}
	sort.Strings(got)
	want := []string{"urn:moos:session:sam.a", "urn:moos:session:sam.b"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Candidates[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestResolveSessionForEnvelope_AbsentNoOccupancy(t *testing.T) {
	state := sessionContextFixture(false)
	// agent:other has no has-occupant relation in the fixture.
	res := ResolveSessionForEnvelope(state, graph.Envelope{
		Actor: "urn:moos:agent:other",
	})
	if res.Kind != ResolveSessionAbsent {
		t.Fatalf("kind = %v, want Absent", res.Kind)
	}
}

// ------------------------------------------------------------------
// SystemInternalEnvelope — §M11 / §M12 allowlist classifier
// ------------------------------------------------------------------

func TestSystemInternalEnvelope_KernelActorPasses(t *testing.T) {
	env := graph.Envelope{
		RewriteType: graph.LINK,
		Actor:       "urn:moos:kernel:hp-z440.primary",
	}
	if !SystemInternalEnvelope(env) {
		t.Errorf("kernel actor should be allowlisted")
	}
}

func TestSystemInternalEnvelope_SweepActorPasses(t *testing.T) {
	// Default sweep actor is urn:moos:kernel:sweep — isKernelActor matches.
	env := graph.Envelope{
		RewriteType:     graph.ADD,
		Actor:           "urn:moos:kernel:sweep",
		TypeID:          "governance_proposal",
		RewriteCategory: graph.WF13,
	}
	if !SystemInternalEnvelope(env) {
		t.Errorf("sweep actor should be allowlisted")
	}
}

func TestSystemInternalEnvelope_InfrastructureADDPasses(t *testing.T) {
	cases := []graph.TypeID{"user", "workstation", "kernel"}
	for _, typeID := range cases {
		env := graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:user:sam", // non-kernel actor allowed for bootstrap
			TypeID:      typeID,
		}
		if !SystemInternalEnvelope(env) {
			t.Errorf("infrastructure ADD type=%s should be allowlisted", typeID)
		}
	}
}

func TestSystemInternalEnvelope_UserADDNotAllowlisted(t *testing.T) {
	// User actor ADDing a non-infrastructure type (e.g. program) must not
	// be allowlisted — this is the ordinary user-space path §M11 governs.
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		TypeID:      "program",
	}
	if SystemInternalEnvelope(env) {
		t.Errorf("user ADD of non-infrastructure type should NOT be allowlisted")
	}
}

func TestSystemInternalEnvelope_UserLINKNotAllowlisted(t *testing.T) {
	env := graph.Envelope{
		RewriteType: graph.LINK,
		Actor:       "urn:moos:user:sam",
		SrcURN:      "urn:moos:session:sam.a",
		TgtURN:      "urn:moos:agent:claude",
	}
	if SystemInternalEnvelope(env) {
		t.Errorf("user-actor LINK should NOT be allowlisted")
	}
}

// ------------------------------------------------------------------
// AdminScopeRewrite — PR 3 plumbing, dormant (always returns false)
// ------------------------------------------------------------------

func TestAdminScopeRewrite_DormantInPR3(t *testing.T) {
	// Until PR 4 fills in the classifier, no envelope is admin-scope-gated.
	// This test pins the dormant state so PR 4 is explicitly a behavior
	// change rather than a silent flip.
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	samples := []graph.Envelope{
		{RewriteType: graph.ADD, Actor: "urn:moos:user:sam", TypeID: "system_instruction"},
		{RewriteType: graph.MUTATE, Actor: "urn:moos:user:sam", TargetURN: "urn:moos:program:x", Field: "status"},
		{RewriteType: graph.LINK, Actor: "urn:moos:user:sam", RewriteCategory: graph.WF01},
	}
	for _, env := range samples {
		if AdminScopeRewrite(env, state) {
			t.Errorf("AdminScopeRewrite should be dormant in PR 3; got true for %+v", env)
		}
	}
}
