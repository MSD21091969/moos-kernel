package hdc_test

import (
	"testing"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
)

func TestSimilarityMatrix_BasicShape(t *testing.T) {
	state := spectralSampleState()
	urns, types, sim, entries := hdc.SimilarityMatrix(state, nil)

	if len(urns) != 3 {
		t.Fatalf("expected 3 urns, got %d", len(urns))
	}
	if len(types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(types))
	}
	if len(sim) != 3 || len(sim[0]) != 3 {
		t.Fatalf("expected 3x3 similarity matrix")
	}
	if len(entries) != 9 {
		t.Fatalf("expected 9 flattened entries, got %d", len(entries))
	}
	if sim[0][0] < 0.999 {
		t.Fatalf("expected self-similarity near 1, got %f", sim[0][0])
	}
}

func TestLaplacianAndEigenDecomposition(t *testing.T) {
	state := spectralSampleState()
	_, _, sim, _ := hdc.SimilarityMatrix(state, nil)
	lap := hdc.Laplacian(sim)
	vals, vecs := hdc.EigenDecompositionSymmetric(lap)

	if len(vals) != 3 {
		t.Fatalf("expected 3 eigenvalues, got %d", len(vals))
	}
	if len(vecs) != 3 || len(vecs[0]) != 3 {
		t.Fatalf("expected 3x3 eigenvector matrix")
	}
	for i := 1; i < len(vals); i++ {
		if vals[i] < vals[i-1] {
			t.Fatalf("eigenvalues not sorted ascending")
		}
	}
	if vals[0] < -1e-6 {
		t.Fatalf("first Laplacian eigenvalue should be non-negative, got %f", vals[0])
	}
}

func TestFiedlerCheegerAndTypeCoherence(t *testing.T) {
	state := spectralSampleState()
	urns, _, sim, _ := hdc.SimilarityMatrix(state, nil)
	lap := hdc.Laplacian(sim)
	vals, vecs := hdc.EigenDecompositionSymmetric(lap)

	fiedler := hdc.FiedlerPartition(urns, vals, vecs)
	if len(fiedler) != len(urns) {
		t.Fatalf("expected fiedler assignments for all urns")
	}
	for _, row := range fiedler {
		if row.Sign != "positive" && row.Sign != "negative" && row.Sign != "zero" {
			t.Fatalf("unexpected sign value %q", row.Sign)
		}
	}

	h := hdc.CheegerConstant(sim, vals, vecs)
	if h < 0 {
		t.Fatalf("cheeger constant should be non-negative, got %f", h)
	}

	coherence := hdc.TypeCoherence(state, nil)
	if len(coherence) != 2 {
		t.Fatalf("expected 2 type coherence entries, got %d", len(coherence))
	}
	for _, group := range coherence {
		if len(group.Nodes) == 0 {
			t.Fatalf("type %s has no nodes", group.TypeID)
		}
	}
}

func TestTypeExpressionsAndDriftFilter(t *testing.T) {
	state := spectralSampleState()
	expressions := hdc.TypeExpressions(state, nil)

	if len(expressions) != 3 {
		t.Fatalf("expected 3 expression rows, got %d", len(expressions))
	}
	for _, row := range expressions {
		if row.URN == "" {
			t.Fatal("expression row has empty urn")
		}
		if row.DeclaredType == "" {
			t.Fatal("expression row has empty declared_type")
		}
		if row.ExpressedType == "" {
			t.Fatal("expression row has empty expressed_type")
		}
		if len(row.Top3Types) == 0 || len(row.Top3Types) > 3 {
			t.Fatalf("expected 1..3 top types for %s, got %d", row.URN, len(row.Top3Types))
		}
	}

	empty := hdc.DriftedTypeExpressions(state, nil, 2.1)
	if len(empty) != 0 {
		t.Fatalf("expected no rows above drift threshold 2.1, got %d", len(empty))
	}

	drifted := hdc.DriftedTypeExpressions(state, nil, 0.3)
	for _, row := range drifted {
		if row.Drift <= 0.3 {
			t.Fatalf("drift filter returned non-drifted row %s with drift %f", row.URN, row.Drift)
		}
	}
}

func TestLiveIndex_RecomputeAndDrifted(t *testing.T) {
	state := spectralSampleState()
	idx := hdc.NewLiveIndex(0.3)
	idx.Recompute(state, nil)

	expressions := idx.Expressions()
	if len(expressions) != 3 {
		t.Fatalf("expected 3 indexed expression rows, got %d", len(expressions))
	}

	drifted := idx.Drifted()
	for _, row := range drifted {
		if row.Drift <= 0.3 {
			t.Fatalf("drifted index returned row below threshold: %s (%f)", row.URN, row.Drift)
		}
	}
}

func spectralSampleState() graph.GraphState {
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
