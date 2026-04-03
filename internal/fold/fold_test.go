package fold_test

import (
	"errors"
	"testing"
	"time"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
)

func userNode(urn graph.URN) graph.Envelope {
	return graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     urn,
		TypeID:      "user",
		Properties: map[string]graph.Property{
			"name":       {Value: "sam", Mutability: "immutable"},
			"created_at": {Value: time.Now().Format(time.RFC3339), Mutability: "immutable"},
		},
	}
}

func TestADD(t *testing.T) {
	state := graph.NewGraphState()
	env := userNode("urn:moos:user:sam")

	next, result, err := fold.Evaluate(state, env)
	if err != nil {
		t.Fatalf("ADD failed: %v", err)
	}
	if result.AffectedNodeURN != "urn:moos:user:sam" {
		t.Errorf("unexpected affected URN: %s", result.AffectedNodeURN)
	}
	if _, ok := next.Nodes["urn:moos:user:sam"]; !ok {
		t.Error("node not present in next state")
	}
	// original state unchanged (immutability of fold)
	if _, ok := state.Nodes["urn:moos:user:sam"]; ok {
		t.Error("fold mutated original state")
	}
}

func TestADD_Idempotent(t *testing.T) {
	state := graph.NewGraphState()
	env := userNode("urn:moos:user:sam")

	state, _, _ = fold.Evaluate(state, env)
	_, _, err := fold.Evaluate(state, env)
	if !errors.Is(err, fold.ErrNodeExists) {
		t.Fatalf("expected ErrNodeExists, got %v", err)
	}
}

func TestLINK(t *testing.T) {
	state := graph.NewGraphState()
	state, _, _ = fold.Evaluate(state, userNode("urn:moos:user:sam"))
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:workstation:hp-laptop",
		TypeID:      "workstation",
		Properties:  map[string]graph.Property{"hostname": {Value: "hp-laptop", Mutability: "immutable"}},
	})

	linkEnv := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF01,
		Actor:           "urn:moos:user:sam",
		RelationURN:     "urn:moos:rel:sam.owns.hp-laptop",
		SrcURN:          "urn:moos:user:sam",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-laptop",
		TgtPort:         "child",
	}

	next, result, err := fold.Evaluate(state, linkEnv)
	if err != nil {
		t.Fatalf("LINK failed: %v", err)
	}
	if result.AffectedRelationURN != "urn:moos:rel:sam.owns.hp-laptop" {
		t.Errorf("unexpected relation URN: %s", result.AffectedRelationURN)
	}
	if _, ok := next.Relations["urn:moos:rel:sam.owns.hp-laptop"]; !ok {
		t.Error("relation not present in next state")
	}
}

func TestLINK_MissingNode(t *testing.T) {
	state := graph.NewGraphState()
	// Add src but not tgt
	state, _, _ = fold.Evaluate(state, userNode("urn:moos:user:sam"))

	_, _, err := fold.Evaluate(state, graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF01,
		Actor:           "urn:moos:user:sam",
		RelationURN:     "urn:moos:rel:sam.owns.hp-laptop",
		SrcURN:          "urn:moos:user:sam",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-laptop", // does not exist
		TgtPort:         "child",
	})
	if !errors.Is(err, fold.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestMUTATE(t *testing.T) {
	state := graph.NewGraphState()
	// Add kernel node with mutable status
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:kernel:hp-laptop.primary",
		TypeID:      "kernel",
		Properties: map[string]graph.Property{
			"version": {Value: "1.0.0", Mutability: "immutable"},
			"status":  {Value: "active", Mutability: "mutable", AuthorityScope: "kernel"},
		},
	})

	next, result, err := fold.Evaluate(state, graph.Envelope{
		RewriteType:     graph.MUTATE,
		RewriteCategory: graph.WF07,
		Actor:           "urn:moos:kernel:hp-laptop.primary",
		TargetURN:       "urn:moos:kernel:hp-laptop.primary",
		Field:           "status",
		NewValue:        "stopped",
	})
	if err != nil {
		t.Fatalf("MUTATE failed: %v", err)
	}
	if result.AffectedNodeURN != "urn:moos:kernel:hp-laptop.primary" {
		t.Errorf("unexpected affected URN: %s", result.AffectedNodeURN)
	}
	node := next.Nodes["urn:moos:kernel:hp-laptop.primary"]
	if node.Properties["status"].Value != "stopped" {
		t.Errorf("status not updated: %v", node.Properties["status"].Value)
	}
}

func TestMUTATE_Immutable(t *testing.T) {
	state := graph.NewGraphState()
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:kernel:hp-laptop.primary",
		TypeID:      "kernel",
		Properties:  map[string]graph.Property{"version": {Value: "1.0.0", Mutability: "immutable"}},
	})

	_, _, err := fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:kernel:hp-laptop.primary",
		Field:       "version",
		NewValue:    "2.0.0",
	})
	if !errors.Is(err, fold.ErrImmutableProperty) {
		t.Fatalf("expected ErrImmutableProperty, got %v", err)
	}
	// Original state unchanged
	if state.Nodes["urn:moos:kernel:hp-laptop.primary"].Properties["version"].Value != "1.0.0" {
		t.Error("immutable property was changed")
	}
}

func TestUNLINK(t *testing.T) {
	state := graph.NewGraphState()
	state, _, _ = fold.Evaluate(state, userNode("urn:moos:user:sam"))
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.ADD, Actor: "urn:moos:user:sam",
		NodeURN: "urn:moos:workstation:hp-laptop", TypeID: "workstation",
		Properties: map[string]graph.Property{"hostname": {Value: "hp-laptop", Mutability: "immutable"}},
	})
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.LINK, RewriteCategory: graph.WF01,
		Actor: "urn:moos:user:sam", RelationURN: "urn:moos:rel:sam.owns.hp-laptop",
		SrcURN: "urn:moos:user:sam", SrcPort: "owns",
		TgtURN: "urn:moos:workstation:hp-laptop", TgtPort: "child",
	})

	next, result, err := fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.UNLINK,
		Actor:       "urn:moos:user:sam",
		RelationURN: "urn:moos:rel:sam.owns.hp-laptop",
	})
	if err != nil {
		t.Fatalf("UNLINK failed: %v", err)
	}
	if result.AffectedRelationURN != "urn:moos:rel:sam.owns.hp-laptop" {
		t.Errorf("unexpected relation URN: %s", result.AffectedRelationURN)
	}
	if _, ok := next.Relations["urn:moos:rel:sam.owns.hp-laptop"]; ok {
		t.Error("relation still present after UNLINK")
	}
	// Original state has the relation (fold is pure)
	if _, ok := state.Relations["urn:moos:rel:sam.owns.hp-laptop"]; !ok {
		t.Error("fold mutated original state on UNLINK")
	}
}

func TestProgram_Atomic(t *testing.T) {
	state := graph.NewGraphState()

	envelopes := []graph.Envelope{
		userNode("urn:moos:user:sam"),
		{RewriteType: graph.ADD, Actor: "urn:moos:user:sam",
			NodeURN: "urn:moos:workstation:hp-laptop", TypeID: "workstation",
			Properties: map[string]graph.Property{"hostname": {Value: "hp-laptop", Mutability: "immutable"}}},
	}
	next, results, err := fold.EvaluateProgram(state, envelopes)
	if err != nil {
		t.Fatalf("program failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if len(next.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(next.Nodes))
	}
}

func TestProgram_Rollback(t *testing.T) {
	state := graph.NewGraphState()

	// Step 0 valid, step 1 has duplicate URN — should rollback entirely
	envelopes := []graph.Envelope{
		userNode("urn:moos:user:sam"),
		userNode("urn:moos:user:sam"), // duplicate
	}
	next, _, err := fold.EvaluateProgram(state, envelopes)
	if err == nil {
		t.Fatal("expected error from duplicate ADD in program")
	}
	// state unchanged (rollback)
	if len(state.Nodes) != 0 {
		t.Error("state was modified despite program rollback")
	}
	if len(next.Nodes) != 0 {
		t.Error("returned state has nodes despite rollback")
	}
}

func TestReplay(t *testing.T) {
	// Build a log via Evaluate
	state := graph.NewGraphState()
	var log []graph.PersistedRewrite
	seq := int64(0)

	apply := func(env graph.Envelope) {
		next, _, err := fold.Evaluate(state, env)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		seq++
		log = append(log, graph.PersistedRewrite{Envelope: env, AppliedAt: time.Now().UTC(), LogSeq: seq})
		state = next
	}

	apply(userNode("urn:moos:user:sam"))
	apply(graph.Envelope{
		RewriteType: graph.ADD, Actor: "urn:moos:user:sam",
		NodeURN: "urn:moos:workstation:hp-laptop", TypeID: "workstation",
		Properties: map[string]graph.Property{"hostname": {Value: "hp-laptop", Mutability: "immutable"}},
	})

	// Replay the log from scratch
	replayed, err := fold.Replay(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed.Nodes) != len(state.Nodes) {
		t.Errorf("replayed %d nodes, expected %d", len(replayed.Nodes), len(state.Nodes))
	}
}

// TestCI3_IdentityStability verifies that MUTATE never changes the node's URN.
func TestCI3_IdentityStability(t *testing.T) {
	state := graph.NewGraphState()
	state, _, _ = fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.ADD, Actor: "urn:moos:user:sam",
		NodeURN: "urn:moos:kernel:hp-laptop.primary", TypeID: "kernel",
		Properties: map[string]graph.Property{
			"status": {Value: "active", Mutability: "mutable", AuthorityScope: "kernel"},
		},
	})

	next, _, err := fold.Evaluate(state, graph.Envelope{
		RewriteType: graph.MUTATE, Actor: "urn:moos:user:sam",
		TargetURN: "urn:moos:kernel:hp-laptop.primary",
		Field:     "status", NewValue: "stopped",
	})
	if err != nil {
		t.Fatalf("MUTATE: %v", err)
	}
	node := next.Nodes["urn:moos:kernel:hp-laptop.primary"]
	if node.URN != "urn:moos:kernel:hp-laptop.primary" {
		t.Errorf("CI-3 violated: URN changed after MUTATE (got %s)", node.URN)
	}
}

// TestCI4_ReplayDeterminism verifies same log → same state always.
func TestCI4_ReplayDeterminism(t *testing.T) {
	env := userNode("urn:moos:user:sam")
	log := []graph.PersistedRewrite{{Envelope: env, AppliedAt: time.Now().UTC(), LogSeq: 1}}

	s1, err1 := fold.Replay(log)
	s2, err2 := fold.Replay(log)

	if err1 != nil || err2 != nil {
		t.Fatalf("replay errors: %v %v", err1, err2)
	}
	n1 := s1.Nodes["urn:moos:user:sam"]
	n2 := s2.Nodes["urn:moos:user:sam"]
	if n1.URN != n2.URN || n1.TypeID != n2.TypeID {
		t.Error("CI-4 violated: same log produced different states")
	}
}
