package operad

import (
	"strings"
	"testing"
	"time"

	"moos/kernel/internal/graph"
)

// buildAcyclicTestState returns a GraphState with the given WF21 causes-edges
// pre-installed. Each edge is (src, tgt) using SrcPort="causes" / TgtPort="caused-by".
// Nodes are auto-stubbed with type "derivation" so the state is internally consistent.
func buildAcyclicTestState(edges [][2]graph.URN) graph.GraphState {
	state := graph.NewGraphState()
	urnSet := map[graph.URN]struct{}{}
	for _, e := range edges {
		urnSet[e[0]] = struct{}{}
		urnSet[e[1]] = struct{}{}
	}
	for urn := range urnSet {
		state.Nodes[urn] = graph.Node{
			URN:    urn,
			TypeID: "derivation",
		}
		graph.IndexAddNodeByType(state.NodesByType, urn, "derivation")
	}
	for i, e := range edges {
		relURN := graph.URN("urn:moos:rel:test." + string(e[0]) + ".causes." + string(e[1]) + "." + string(rune('a'+i)))
		state.Relations[relURN] = graph.Relation{
			URN:             relURN,
			RewriteCategory: graph.RewriteCategory("WF21"),
			SrcURN:          e[0],
			SrcPort:         "causes",
			TgtURN:          e[1],
			TgtPort:         "caused-by",
			CreatedAt:       time.Now(),
		}
		graph.IndexAddRelationEndpoints(state.RelationsBySrc, state.RelationsByTgt, relURN, e[0], e[1])
	}
	return state
}

func TestValidateCausalAcyclic_NonWF21LinkPasses(t *testing.T) {
	reg := EmptyRegistry()
	state := graph.NewGraphState()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF18, // not WF21
		SrcURN:          "urn:moos:claim:a",
		TgtURN:          "urn:moos:claim:b",
		SrcPort:         "annotates",
		TgtPort:         "annotated-by",
	}
	if err := reg.ValidateCausalAcyclic(env, state); err != nil {
		t.Fatalf("non-WF21 LINK should pass: got %v", err)
	}
}

func TestValidateCausalAcyclic_NonLinkPasses(t *testing.T) {
	reg := EmptyRegistry()
	state := graph.NewGraphState()
	for _, rt := range []graph.RewriteType{graph.ADD, graph.MUTATE, graph.UNLINK} {
		env := graph.Envelope{
			RewriteType:     rt,
			RewriteCategory: graph.RewriteCategory("WF21"),
			SrcURN:          "urn:moos:a",
			TgtURN:          "urn:moos:b",
		}
		if err := reg.ValidateCausalAcyclic(env, state); err != nil {
			t.Fatalf("non-LINK rewrite %v should pass: got %v", rt, err)
		}
	}
}

func TestValidateCausalAcyclic_SelfLinkRejected(t *testing.T) {
	reg := EmptyRegistry()
	state := graph.NewGraphState()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:self",
		TgtURN:          "urn:moos:claim:self",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	err := reg.ValidateCausalAcyclic(env, state)
	if err == nil {
		t.Fatal("self-LINK should reject as 1-cycle")
	}
	if !strings.Contains(err.Error(), "1-cycle") {
		t.Fatalf("expected '1-cycle' in error, got %v", err)
	}
}

func TestValidateCausalAcyclic_NoPathPasses(t *testing.T) {
	// A → B exists; new edge C → D should pass (no path D...→C exists).
	reg := EmptyRegistry()
	state := buildAcyclicTestState([][2]graph.URN{
		{"urn:moos:claim:a", "urn:moos:claim:b"},
	})
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:c",
		TgtURN:          "urn:moos:claim:d",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	if err := reg.ValidateCausalAcyclic(env, state); err != nil {
		t.Fatalf("disjoint edge should pass: got %v", err)
	}
}

func TestValidateCausalAcyclic_DirectCycleRejected(t *testing.T) {
	// B → A exists; new edge A → B would close a 2-cycle.
	reg := EmptyRegistry()
	state := buildAcyclicTestState([][2]graph.URN{
		{"urn:moos:claim:b", "urn:moos:claim:a"},
	})
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:a",
		TgtURN:          "urn:moos:claim:b",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	err := reg.ValidateCausalAcyclic(env, state)
	if err == nil {
		t.Fatal("direct cycle should reject")
	}
	if !strings.Contains(err.Error(), "close a cycle") {
		t.Fatalf("expected 'close a cycle' in error, got %v", err)
	}
}

func TestValidateCausalAcyclic_TransitiveCycleRejected(t *testing.T) {
	// B → C → D → A exists; new edge A → B would close a 4-cycle.
	reg := EmptyRegistry()
	state := buildAcyclicTestState([][2]graph.URN{
		{"urn:moos:claim:b", "urn:moos:claim:c"},
		{"urn:moos:claim:c", "urn:moos:claim:d"},
		{"urn:moos:claim:d", "urn:moos:claim:a"},
	})
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:a",
		TgtURN:          "urn:moos:claim:b",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	err := reg.ValidateCausalAcyclic(env, state)
	if err == nil {
		t.Fatal("transitive cycle should reject")
	}
	if !strings.Contains(err.Error(), "close a cycle") {
		t.Fatalf("expected 'close a cycle' in error, got %v", err)
	}
}

func TestValidateCausalAcyclic_DAGForkPasses(t *testing.T) {
	// B → C, B → D (fork); new edge A → B should pass — no path B...→A.
	reg := EmptyRegistry()
	state := buildAcyclicTestState([][2]graph.URN{
		{"urn:moos:claim:b", "urn:moos:claim:c"},
		{"urn:moos:claim:b", "urn:moos:claim:d"},
	})
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:a",
		TgtURN:          "urn:moos:claim:b",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	if err := reg.ValidateCausalAcyclic(env, state); err != nil {
		t.Fatalf("DAG fork should pass: got %v", err)
	}
}

func TestValidateCausalAcyclic_NonCausesPortIgnored(t *testing.T) {
	// A WF21 relation but with non-"causes" SrcPort should not be followed
	// (defensive — only true causes-direction edges count).
	reg := EmptyRegistry()
	state := graph.NewGraphState()
	for _, urn := range []graph.URN{"urn:moos:claim:a", "urn:moos:claim:b"} {
		state.Nodes[urn] = graph.Node{URN: urn, TypeID: "derivation"}
	}
	relURN := graph.URN("urn:moos:rel:reverse-only")
	state.Relations[relURN] = graph.Relation{
		URN:             relURN,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:b",
		SrcPort:         "caused-by", // reverse direction, not "causes"
		TgtURN:          "urn:moos:claim:a",
		TgtPort:         "causes",
	}
	graph.IndexAddRelationEndpoints(state.RelationsBySrc, state.RelationsByTgt, relURN, "urn:moos:claim:b", "urn:moos:claim:a")

	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.RewriteCategory("WF21"),
		SrcURN:          "urn:moos:claim:a",
		TgtURN:          "urn:moos:claim:b",
		SrcPort:         "causes",
		TgtPort:         "caused-by",
	}
	if err := reg.ValidateCausalAcyclic(env, state); err != nil {
		t.Fatalf("non-causes port should not be followed; expected pass, got %v", err)
	}
}
