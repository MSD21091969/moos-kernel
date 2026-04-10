package hdc_test

import (
	"math"
	"testing"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
)

func TestComputeCrosswalk_Basic(t *testing.T) {
	state := crosswalkSampleState()
	res, ok := hdc.ComputeCrosswalk(state, "arxiv", "lcc", nil)
	if !ok {
		t.Fatal("expected crosswalk result for arxiv->lcc")
	}
	if res.Dimension != 64 {
		t.Fatalf("expected reduced dimension 64, got %d", res.Dimension)
	}
	if len(res.Matrix) != 64 || len(res.Matrix[0]) != 64 {
		t.Fatalf("expected 64x64 rotation matrix")
	}
	if math.IsNaN(res.ResidualError) || math.IsInf(res.ResidualError, 0) {
		t.Fatalf("invalid residual error: %f", res.ResidualError)
	}
}

func TestCrosswalkCompositionAndClassificationSpace(t *testing.T) {
	state := crosswalkSampleState()

	checks := hdc.CrosswalkCompositionChecks(state, nil)
	if len(checks) != 6 {
		t.Fatalf("expected 6 chain checks for 3 schemes, got %d", len(checks))
	}
	for _, row := range checks {
		if row.Chain == "" {
			t.Fatal("composition check has empty chain")
		}
		if row.Error < 0 {
			t.Fatalf("composition error should be non-negative, got %f", row.Error)
		}
	}

	space := hdc.ClassificationSpace(state, nil)
	if len(space.Points) != 3 {
		t.Fatalf("expected 3 PCA points, got %d", len(space.Points))
	}
	if len(space.ExplainedVariance) == 0 || len(space.ExplainedVariance) > 3 {
		t.Fatalf("unexpected explained variance length %d", len(space.ExplainedVariance))
	}
	for _, p := range space.Points {
		if p.Scheme == "" {
			t.Fatal("classification point missing scheme name")
		}
	}
}

func TestCrosswalkSuggestions_ThresholdCutoff(t *testing.T) {
	state := crosswalkSampleState()
	suggestions := hdc.CrosswalkSuggestions(state, nil, 1.1)
	if len(suggestions) != 0 {
		t.Fatalf("expected no suggestions above cosine threshold 1.1, got %d", len(suggestions))
	}
}

func crosswalkSampleState() graph.GraphState {
	state := graph.NewGraphState()

	state.Nodes["urn:moos:tag:arxiv.cs-ai"] = graph.Node{
		URN:    "urn:moos:tag:arxiv.cs-ai",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "arxiv", Mutability: "immutable"},
			"name":   {Value: "cs-ai", Mutability: "immutable"},
		},
	}
	state.Nodes["urn:moos:tag:arxiv.math-ct"] = graph.Node{
		URN:    "urn:moos:tag:arxiv.math-ct",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "arxiv", Mutability: "immutable"},
			"name":   {Value: "math-ct", Mutability: "immutable"},
		},
	}
	state.Nodes["urn:moos:tag:lcc.qa"] = graph.Node{
		URN:    "urn:moos:tag:lcc.qa",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "lcc", Mutability: "immutable"},
			"name":   {Value: "qa", Mutability: "immutable"},
		},
	}
	state.Nodes["urn:moos:tag:lcc.qh"] = graph.Node{
		URN:    "urn:moos:tag:lcc.qh",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "lcc", Mutability: "immutable"},
			"name":   {Value: "qh", Mutability: "immutable"},
		},
	}
	state.Nodes["urn:moos:tag:iso.9001"] = graph.Node{
		URN:    "urn:moos:tag:iso.9001",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "iso", Mutability: "immutable"},
			"name":   {Value: "9001", Mutability: "immutable"},
		},
	}
	state.Nodes["urn:moos:tag:iso.27001"] = graph.Node{
		URN:    "urn:moos:tag:iso.27001",
		TypeID: "domain_tag",
		Properties: map[string]graph.Property{
			"scheme": {Value: "iso", Mutability: "immutable"},
			"name":   {Value: "27001", Mutability: "immutable"},
		},
	}

	return state
}
