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
// AdminScopeRewrite — §M12 classifier (PR 4)
// ------------------------------------------------------------------

// adminScopeRegistry returns a Registry with the node-type specs PR 4
// exercises: `program` (ordinary, owner-authority), `session` (kernel-
// authority on context_urn + local_t), and the five ontology-governed
// types whose touches are always admin-scope.
func adminScopeRegistry() *Registry {
	reg := EmptyRegistry()
	reg.NodeTypes["program"] = NodeTypeSpec{
		ID: "program", Stratum: "S2",
		Properties: map[string]PropertySpec{
			"status":    {Mutability: "mutable", AuthorityScope: "owner"},
			"target_t":  {Mutability: "mutable", AuthorityScope: "kernel"},
			"scope":     {Mutability: "mutable", AuthorityScope: "owner"},
			"owner_urn": {Mutability: "immutable", AuthorityScope: ""},
		},
	}
	reg.NodeTypes["session"] = NodeTypeSpec{
		ID: "session", Stratum: "S2",
		Properties: map[string]PropertySpec{
			"context_urn": {Mutability: "mutable", AuthorityScope: "kernel"},
			"local_t":     {Mutability: "mutable", AuthorityScope: "kernel"},
			"view_prefs":  {Mutability: "mutable", AuthorityScope: "owner"},
		},
	}
	for _, t := range []graph.TypeID{"system_instruction", "gate", "twin_link", "transport_binding", "kernel"} {
		reg.NodeTypes[t] = NodeTypeSpec{ID: t, Stratum: "S2"}
	}
	return reg
}

func TestAdminScopeRewrite_ADDOntologyGoverned_AllTypes(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	for _, typeID := range []graph.TypeID{"system_instruction", "gate", "twin_link", "transport_binding", "kernel"} {
		env := graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:user:sam",
			TypeID:      typeID,
			NodeURN:     graph.URN("urn:moos:" + string(typeID) + ":test"),
		}
		if !reg.AdminScopeRewrite(env, state) {
			t.Errorf("ADD of ontology-governed type %s should be admin-scope", typeID)
		}
	}
}

func TestAdminScopeRewrite_ADDOrdinaryType_NotAdminScope(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	env := graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		TypeID:      "program",
		NodeURN:     "urn:moos:program:x",
	}
	if reg.AdminScopeRewrite(env, state) {
		t.Errorf("ADD of ordinary type (program) should NOT be admin-scope")
	}
}

func TestAdminScopeRewrite_MUTATEOntologyGovernedNode_AdminScope(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:gate:approval": {URN: "urn:moos:gate:approval", TypeID: "gate"},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:gate:approval",
		Field:       "predicate_expr",
		NewValue:    "true",
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("MUTATE of gate node (ontology-governed) should be admin-scope")
	}
}

func TestAdminScopeRewrite_MUTATEKernelAuthorityField_AdminScope(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:x": {
				URN: "urn:moos:program:x", TypeID: "program",
				Properties: map[string]graph.Property{
					"target_t": {Value: 190.0, Mutability: "mutable", AuthorityScope: "kernel"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam", // non-kernel actor
		TargetURN:   "urn:moos:program:x",
		Field:       "target_t", // authority_scope=kernel
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("MUTATE of kernel-authority field on non-kernel node should be admin-scope")
	}
}

// TestAdminScopeRewrite_MUTATEKernelAuthorityField_AdditiveLookup exercises the
// registry-type-spec fallback path for an additive MUTATE: the field is not
// yet on the node, so the classifier consults the type spec's AuthorityScope
// declaration directly.
func TestAdminScopeRewrite_MUTATEKernelAuthorityField_AdditiveLookup(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:y": {
				URN: "urn:moos:program:y", TypeID: "program",
				Properties: map[string]graph.Property{}, // target_t not yet on node
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:program:y",
		Field:       "target_t", // kernel-authority in type spec
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("additive MUTATE of kernel-authority field should be admin-scope via type-spec lookup")
	}
}

func TestAdminScopeRewrite_MUTATEOwnerAuthorityField_NotAdminScope(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:x": {
				URN: "urn:moos:program:x", TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable", AuthorityScope: "owner"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:program:x",
		Field:       "status", // authority_scope=owner
	}
	if reg.AdminScopeRewrite(env, state) {
		t.Errorf("MUTATE of owner-authority field should NOT be admin-scope")
	}
}

func TestAdminScopeRewrite_MUTATEOnKernelNode_AdminScopeViaOntologyGovernedType(t *testing.T) {
	// MUTATE on a kernel-typed node is admin-scope via case 1 (kernel is in
	// ontology-governed types). Test documents this explicitly so reading
	// the suite clarifies that kernel-nodes-are-always-admin-scope follows
	// from the ontology-governed-type rule, not from the kernel-authority-
	// field rule (which explicitly excludes kernel-typed targets).
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:kernel:hp-z440.primary": {
				URN: "urn:moos:kernel:hp-z440.primary", TypeID: "kernel",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable", AuthorityScope: "kernel"},
				},
			},
		},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:kernel:hp-z440.primary",
		Field:       "status",
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("MUTATE on kernel-typed node should be admin-scope via ontology-governed-type rule")
	}
}

// TestAdminScopeRewrite_MUTATETargetMissing_FailsClosed pins the Gemini
// security-HIGH fix: a MUTATE whose target is absent from the passed state
// is classified admin-scope (fail-closed). A lone missing-target MUTATE
// will fold-fail with ErrNodeNotFound regardless; the real purpose is to
// prevent an ADD-then-MUTATE batch from bypassing §M12 when the classifier
// is called against the batch-initial state. ApplyProgram threads
// workingState through the §M12 check so normal ADD+MUTATE batches DO see
// the just-created node and classify correctly — this test pins the
// behavior when the caller does NOT thread working-state.
func TestAdminScopeRewrite_MUTATETargetMissing_FailsClosed(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:program:not-in-state",
		Field:       "status",
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("MUTATE on missing target must fail closed (admin-scope=true) to prevent ADD+MUTATE bypass")
	}
}

// TestAdminScopeRewrite_EmptyStoredAuthorityScope_FallsBackToTypeSpec pins
// the Gemini security-HIGH fix on authorityScopeForField: when a node's
// stored property has an empty AuthorityScope (e.g. an older ADD that
// omitted the metadata), the classifier falls back to the registry type
// spec rather than trusting the blank stored value. This prevents an ADD
// that skipped authority_scope from indefinitely bypassing §M12 on its
// kernel-authority properties.
func TestAdminScopeRewrite_EmptyStoredAuthorityScope_FallsBackToTypeSpec(t *testing.T) {
	reg := adminScopeRegistry()
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:legacy": {
				URN: "urn:moos:program:legacy", TypeID: "program",
				Properties: map[string]graph.Property{
					// Property present but AuthorityScope blank — simulates
					// an ADD that didn't populate the metadata correctly.
					"target_t": {Value: 100.0, Mutability: "mutable", AuthorityScope: ""},
				},
			},
		},
	}
	env := graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:program:legacy",
		Field:       "target_t", // type spec declares authority_scope=kernel
	}
	if !reg.AdminScopeRewrite(env, state) {
		t.Errorf("empty stored AuthorityScope should fall back to type spec (target_t is kernel-authority)")
	}
}

func TestAdminScopeRewrite_LINK_NotAdminScope(t *testing.T) {
	// LINK / UNLINK are not admin-scope in §M12 v1. The doctrine gates on
	// node-level operations; relation-level admin-gating would be a v2
	// extension with its own doctrine note.
	reg := adminScopeRegistry()
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		Actor:           "urn:moos:user:sam",
		RelationURN:     "urn:moos:rel:test",
		SrcURN:          "urn:moos:session:sam.a",
		TgtURN:          "urn:moos:gate:approval", // ontology-governed tgt
		RewriteCategory: graph.WF19,
	}
	if reg.AdminScopeRewrite(env, state) {
		t.Errorf("LINK should not be admin-scope in §M12 v1")
	}
}

func TestAdminScopeRewrite_NilRegistry(t *testing.T) {
	// Nil-receiver safety: matches the pattern used elsewhere (ValidateLINK,
	// ValidateMUTATE, checkLiveness all short-circuit on nil registry).
	var r *Registry
	state := graph.GraphState{Nodes: map[graph.URN]graph.Node{}, Relations: map[graph.URN]graph.Relation{}}
	env := graph.Envelope{RewriteType: graph.ADD, TypeID: "gate"}
	if r.AdminScopeRewrite(env, state) {
		t.Errorf("nil registry should return false")
	}
}
