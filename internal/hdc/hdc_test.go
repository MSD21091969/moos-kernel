package hdc_test

import (
	"testing"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
)

func TestBind_Invertible(t *testing.T) {
	cb := hdc.NewCodebook()
	a := cb.Encode("urn:moos:test:a")
	b := cb.Encode("urn:moos:test:b")

	bound := hdc.Bind(a, b)
	got := hdc.Unbind(bound, a)
	if sim := hdc.Cosine(got, b); sim < 0.999 {
		t.Fatalf("unbind failed, cosine=%f", sim)
	}
}

func TestBundle_SimilarToInputs(t *testing.T) {
	cb := hdc.NewCodebook()
	a := cb.Encode("urn:moos:test:a")
	b := cb.Encode("urn:moos:test:b")

	bundled := hdc.Bundle(a, b)
	if sim := hdc.Cosine(bundled, a); sim <= 0 {
		t.Fatalf("bundle not similar to a, cosine=%f", sim)
	}
	if sim := hdc.Cosine(bundled, b); sim <= 0 {
		t.Fatalf("bundle not similar to b, cosine=%f", sim)
	}
}

func TestPermute_Dissimilar(t *testing.T) {
	cb := hdc.NewCodebook()
	v := cb.Encode("urn:moos:test:v")
	p := hdc.Permute(v, 1)
	if sim := hdc.Cosine(p, v); sim >= 0.1 {
		t.Fatalf("permute too similar, cosine=%f", sim)
	}
}

func TestEncodeNode_SameType_Similar(t *testing.T) {
	state := sampleState()
	enc := hdc.NewEncoder()

	sam := enc.EncodeNode(state, "urn:moos:user:sam")
	menno := enc.EncodeNode(state, "urn:moos:user:menno")
	if sim := hdc.Cosine(sam, menno); sim <= 0.5 {
		t.Fatalf("same-type nodes not similar enough, cosine=%f", sim)
	}
}

func TestEncodeNode_DifferentType_Orthogonal(t *testing.T) {
	state := sampleState()
	enc := hdc.NewEncoder()

	sam := enc.EncodeNode(state, "urn:moos:user:sam")
	ws := enc.EncodeNode(state, "urn:moos:workstation:hp-z440")
	if sim := hdc.Cosine(sam, ws); sim >= 0.1 {
		t.Fatalf("different-type nodes too similar, cosine=%f", sim)
	}
}

func sampleState() graph.GraphState {
	state := graph.NewGraphState()

	state.Nodes["urn:moos:user:sam"] = graph.Node{
		URN:        "urn:moos:user:sam",
		TypeID:     "user",
		Properties: map[string]graph.Property{"name": {Value: "sam", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:user:menno"] = graph.Node{
		URN:        "urn:moos:user:menno",
		TypeID:     "user",
		Properties: map[string]graph.Property{"name": {Value: "menno", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:workstation:hp-z440"] = graph.Node{
		URN:        "urn:moos:workstation:hp-z440",
		TypeID:     "workstation",
		Properties: map[string]graph.Property{"hostname": {Value: "hp-z440", Mutability: "immutable"}},
	}

	state.Relations["urn:moos:rel:sam.owns.z440"] = graph.Relation{
		URN:             "urn:moos:rel:sam.owns.z440",
		RewriteCategory: graph.WF01,
		SrcURN:          "urn:moos:user:sam",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-z440",
		TgtPort:         "child",
	}
	state.Relations["urn:moos:rel:menno.owns.z440"] = graph.Relation{
		URN:             "urn:moos:rel:menno.owns.z440",
		RewriteCategory: graph.WF01,
		SrcURN:          "urn:moos:user:menno",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-z440",
		TgtPort:         "child",
	}

	return state
}
