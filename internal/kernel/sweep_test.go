package kernel

import (
	"strings"
	"testing"

	"moos/kernel/internal/graph"
)

// sweepState returns a graph state with:
//   - urn:moos:program:sam.prog  (status=active)
//   - urn:moos:t_hook:sam.hook-a (fires_at=200, no pending proposal)
//   - urn:moos:t_hook:sam.hook-b (fires_at=300, no pending proposal — never fires at T=250)
//   - urn:moos:t_hook:sam.hook-c (fires_at=180 but already has a governance_proposal)
func sweepState() graph.GraphState {
	return graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.prog": {
				URN:    "urn:moos:program:sam.prog",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
				},
			},
			"urn:moos:t_hook:sam.hook-a": {
				URN:    "urn:moos:t_hook:sam.hook-a",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 200}, Mutability: "immutable"},
					"react_template": {
						Value:      map[string]any{"rewrite_type": "MUTATE", "target_urn": "urn:moos:program:sam.prog", "field": "status", "new_value": "done"},
						Mutability: "mutable",
					},
				},
			},
			"urn:moos:t_hook:sam.hook-b": {
				URN:    "urn:moos:t_hook:sam.hook-b",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 300}, Mutability: "immutable"},
				},
			},
			"urn:moos:t_hook:sam.hook-c": {
				URN:    "urn:moos:t_hook:sam.hook-c",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 180}, Mutability: "immutable"},
				},
			},
			// Proposal already exists for hook-c — idempotency marker.
			"urn:moos:proposal:kernel.hook-c-t180-seq0": {
				URN:    "urn:moos:proposal:kernel.hook-c-t180-seq0",
				TypeID: "governance_proposal",
				Properties: map[string]graph.Property{
					"title":             {Value: "Fire t_hook urn:moos:t_hook:sam.hook-c at T=180", Mutability: "immutable"},
					"source_t_hook_urn": {Value: "urn:moos:t_hook:sam.hook-c", Mutability: "immutable"},
					"fires_at_t":        {Value: 180, Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
}

// TestSweepOnce_FiresMaturedHook — at T=250, hook-a (fires_at=200) fires
// and produces a governance_proposal envelope. hook-b (fires_at=300) does
// not. hook-c already has a proposal and is skipped.
func TestSweepOnce_FiresMaturedHook(t *testing.T) {
	state := sweepState()
	actor := graph.URN("urn:moos:kernel:test.sweep")
	envelopes := SweepOnce(state, 250, actor, 0)

	if len(envelopes) != 1 {
		t.Fatalf("expected exactly 1 envelope (hook-a ADD governance_proposal); got %d:\n%v", len(envelopes), envelopes)
	}
	env := envelopes[0]
	if env.RewriteType != graph.ADD {
		t.Errorf("expected ADD, got %s", env.RewriteType)
	}
	if env.TypeID != "governance_proposal" {
		t.Errorf("expected governance_proposal, got %s", env.TypeID)
	}
	if env.Actor != actor {
		t.Errorf("expected actor %s, got %s", actor, env.Actor)
	}
	// Check the proposal references hook-a.
	srcHookProp := env.Properties["source_t_hook_urn"]
	if s, _ := srcHookProp.Value.(string); s != "urn:moos:t_hook:sam.hook-a" {
		t.Errorf("expected source_t_hook_urn=hook-a, got %v", srcHookProp.Value)
	}
	// fires_at_t should be 250 (the T we evaluated at).
	if fireT, _ := env.Properties["fires_at_t"].Value.(int); fireT != 250 {
		t.Errorf("expected fires_at_t=250, got %v", env.Properties["fires_at_t"].Value)
	}
	// title required immutable.
	titleProp, hasTitle := env.Properties["title"]
	if !hasTitle {
		t.Error("expected title property on governance_proposal (required immutable)")
	}
	if titleProp.Mutability != "immutable" {
		t.Errorf("expected title.Mutability=immutable, got %s", titleProp.Mutability)
	}
	// URN pattern check (urn:moos:proposal:<user>.<slug>).
	if !strings.HasPrefix(string(env.NodeURN), "urn:moos:proposal:") {
		t.Errorf("expected node_urn to start with urn:moos:proposal:, got %s", env.NodeURN)
	}
	// Must mention hook slug for traceability.
	if !strings.Contains(string(env.NodeURN), "hook-a") {
		t.Errorf("expected node_urn to contain hook-a slug, got %s", env.NodeURN)
	}
}

// TestSweepOnce_IdempotencyViaExistingProposal — hook-c already has a
// governance_proposal with source_t_hook_urn pointing at it, so the sweep
// must NOT emit a duplicate. This is the idempotency mechanism for
// v3.9-era kernels where firing_state isn't in the t_hook spec.
func TestSweepOnce_IdempotencyViaExistingProposal(t *testing.T) {
	state := sweepState()
	// Use T=200 so hook-a fires but hook-c (already proposed) is still pending.
	envelopes := SweepOnce(state, 200, "urn:moos:kernel:test.sweep", 0)

	// Exactly one envelope (for hook-a). hook-c must be skipped despite
	// being matured.
	for _, env := range envelopes {
		if s, _ := env.Properties["source_t_hook_urn"].Value.(string); s == "urn:moos:t_hook:sam.hook-c" {
			t.Errorf("expected hook-c to be skipped (already has proposal); got envelope %+v", env)
		}
	}
}

// TestSweepOnce_NoFiresNoEnvelopes — at T=0 no hook has matured, so the
// sweep returns an empty envelope slice.
func TestSweepOnce_NoFiresNoEnvelopes(t *testing.T) {
	state := sweepState()
	envelopes := SweepOnce(state, 0, "urn:moos:kernel:test.sweep", 0)

	if len(envelopes) != 0 {
		t.Errorf("expected 0 envelopes at T=0; got %d:\n%v", len(envelopes), envelopes)
	}
}

// TestSweepOnce_AllFire — at T=1000 every hook has matured; hook-a and
// hook-b are proposed (hook-c already is). Order doesn't matter but both
// hook-a and hook-b envelopes must be present and correctly shaped.
func TestSweepOnce_AllFire(t *testing.T) {
	state := sweepState()
	envelopes := SweepOnce(state, 1000, "urn:moos:kernel:test.sweep", 0)

	hooks := map[string]bool{}
	for _, env := range envelopes {
		s, _ := env.Properties["source_t_hook_urn"].Value.(string)
		hooks[s] = true
	}

	if !hooks["urn:moos:t_hook:sam.hook-a"] {
		t.Errorf("expected proposal for hook-a at T=1000")
	}
	if !hooks["urn:moos:t_hook:sam.hook-b"] {
		t.Errorf("expected proposal for hook-b at T=1000")
	}
	if hooks["urn:moos:t_hook:sam.hook-c"] {
		t.Errorf("hook-c must be skipped (already proposed)")
	}
}

// TestSweepOnce_ProposalCarriesReactTemplate — the proposal envelope
// carries the source hook's react_template so approvers can see exactly
// what rewrite would apply if they approve.
func TestSweepOnce_ProposalCarriesReactTemplate(t *testing.T) {
	state := sweepState()
	envelopes := SweepOnce(state, 250, "urn:moos:kernel:test.sweep", 0)

	if len(envelopes) == 0 {
		t.Fatalf("expected at least one envelope")
	}
	env := envelopes[0]
	rtProp, has := env.Properties["proposed_envelope"]
	if !has {
		t.Fatalf("expected proposed_envelope property")
	}
	tmpl, ok := rtProp.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected proposed_envelope to be a map[string]any, got %T", rtProp.Value)
	}
	if tmpl["rewrite_type"] != "MUTATE" {
		t.Errorf("expected proposed_envelope.rewrite_type=MUTATE, got %v", tmpl["rewrite_type"])
	}
	if tmpl["target_urn"] != "urn:moos:program:sam.prog" {
		t.Errorf("expected proposed_envelope.target_urn=prog, got %v", tmpl["target_urn"])
	}
}

// TestSweepOnce_SkipsHooksWithoutPredicate — a t_hook node missing the
// `predicate` property is silently skipped (no envelope, no error).
func TestSweepOnce_SkipsHooksWithoutPredicate(t *testing.T) {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:t_hook:sam.naked": {
				URN:    "urn:moos:t_hook:sam.naked",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.x", Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}

	envelopes := SweepOnce(state, 1000, "urn:moos:kernel:test.sweep", 0)
	if len(envelopes) != 0 {
		t.Errorf("expected 0 envelopes (hook has no predicate); got %d", len(envelopes))
	}
}

// TestSweepOnce_HandlesCompoundPredicate — a t_hook with an all_of
// compound predicate fires only when every sub-predicate holds. Mirrors
// the round-8 v310-delivery.startable hook shape.
func TestSweepOnce_HandlesCompoundPredicate(t *testing.T) {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.anchor": {
				URN:    "urn:moos:program:sam.anchor",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
				},
			},
			"urn:moos:t_hook:sam.compound": {
				URN:    "urn:moos:t_hook:sam.compound",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.downstream", Mutability: "immutable"},
					"predicate": {
						Value: map[string]any{
							"kind": "all_of",
							"predicates": []any{
								map[string]any{"kind": "fires_at", "t": 220},
								map[string]any{
									"kind":  "after_urn",
									"urn":   "urn:moos:program:sam.anchor",
									"prop":  "status",
									"value": "completed",
								},
							},
						},
						Mutability: "immutable",
					},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}

	// At T=220 with anchor still active → compound is false → no fire.
	if envelopes := SweepOnce(state, 220, "urn:moos:kernel:test.sweep", 0); len(envelopes) != 0 {
		t.Errorf("compound predicate should be false (anchor.status=active != completed); got %d envelopes", len(envelopes))
	}

	// Flip anchor to completed; both clauses now true → compound fires.
	anchor := state.Nodes["urn:moos:program:sam.anchor"]
	anchor.Properties["status"] = graph.Property{Value: "completed", Mutability: "mutable"}
	state.Nodes["urn:moos:program:sam.anchor"] = anchor

	envelopes := SweepOnce(state, 220, "urn:moos:kernel:test.sweep", 0)
	if len(envelopes) != 1 {
		t.Fatalf("compound predicate satisfied; expected 1 envelope, got %d", len(envelopes))
	}
	if s, _ := envelopes[0].Properties["source_t_hook_urn"].Value.(string); s != "urn:moos:t_hook:sam.compound" {
		t.Errorf("expected source_t_hook_urn=compound hook, got %v", envelopes[0].Properties["source_t_hook_urn"].Value)
	}
}

// TestSweepOnce_UniqueProposalURNsAcrossHooks — when two hooks fire in
// the same tick they must get distinct proposal URNs. Uses logSeq offset
// inside the URN so ApplyProgram's all-or-nothing atomicity doesn't see
// duplicate ADDs.
func TestSweepOnce_UniqueProposalURNsAcrossHooks(t *testing.T) {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.p": {
				URN: "urn:moos:program:sam.p", TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
				},
			},
			"urn:moos:t_hook:sam.h1": {
				URN: "urn:moos:t_hook:sam.h1", TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.p", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 100}, Mutability: "immutable"},
				},
			},
			"urn:moos:t_hook:sam.h2": {
				URN: "urn:moos:t_hook:sam.h2", TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.p", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 100}, Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}

	envelopes := SweepOnce(state, 200, "urn:moos:kernel:test.sweep", 100)
	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes for 2 matured hooks; got %d", len(envelopes))
	}
	if envelopes[0].NodeURN == envelopes[1].NodeURN {
		t.Errorf("expected distinct proposal URNs; got duplicates: %s", envelopes[0].NodeURN)
	}
}
