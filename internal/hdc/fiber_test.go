package hdc_test

import (
	"testing"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
)

func TestEncodeFiberAndFederationVectors(t *testing.T) {
	state := fiberSampleState()
	kernels := hdc.KernelURNs(state)
	if len(kernels) != 4 {
		t.Fatalf("expected 4 kernels, got %d", len(kernels))
	}

	fiber := hdc.FiberVectorForKernel(state, "urn:moos:kernel:hp-z440.menno", nil)
	if fiber.Kernel != "urn:moos:kernel:hp-z440.menno" {
		t.Fatalf("unexpected kernel in fiber response: %s", fiber.Kernel)
	}
	if fiber.NodeCount == 0 {
		t.Fatal("expected non-empty menno fiber")
	}
	if len(fiber.Vector) != hdc.Dimension {
		t.Fatalf("expected vector dimension %d, got %d", hdc.Dimension, len(fiber.Vector))
	}

	fed := hdc.FederationVector(state, nil)
	if fed.KernelCount != 4 {
		t.Fatalf("expected federation of 4 kernels, got %d", fed.KernelCount)
	}
	if len(fed.Vector) != hdc.Dimension {
		t.Fatalf("expected vector dimension %d, got %d", hdc.Dimension, len(fed.Vector))
	}
}

func TestFiberAssignments_CurrentAndOptimal(t *testing.T) {
	state := fiberSampleState()
	rows := hdc.FiberAssignments(state, nil)
	if len(rows) != len(state.Nodes) {
		t.Fatalf("expected assignment row per node: got %d want %d", len(rows), len(state.Nodes))
	}

	index := make(map[graph.URN]hdc.FiberAssignmentEntry, len(rows))
	for _, row := range rows {
		if row.CurrentKernel == "" || row.OptimalKernel == "" {
			t.Fatalf("empty kernel assignment for node %s", row.URN)
		}
		if row.Distance < 0 {
			t.Fatalf("negative distance for node %s", row.URN)
		}
		index[row.URN] = row
	}

	if got := index["urn:moos:user:menno"].CurrentKernel; got != "urn:moos:kernel:hp-z440.menno" {
		t.Fatalf("expected menno user on menno kernel, got %s", got)
	}
	if got := index["urn:moos:user:lola"].CurrentKernel; got != "urn:moos:kernel:hp-z440.lola" {
		t.Fatalf("expected lola user on lola kernel, got %s", got)
	}
}

func TestFiberDistribution_JSDRange(t *testing.T) {
	state := fiberSampleState()
	dist := hdc.FiberDistribution(state)
	if len(dist) != 4 {
		t.Fatalf("expected 4 kernel distribution rows, got %d", len(dist))
	}
	for _, row := range dist {
		if len(row.TypeHistogram) == 0 {
			t.Fatalf("expected non-empty type histogram for kernel %s", row.Kernel)
		}
		if row.JSDToFederation < 0 || row.JSDToFederation > 1 {
			t.Fatalf("JSD out of range for kernel %s: %f", row.Kernel, row.JSDToFederation)
		}
	}
}

func fiberSampleState() graph.GraphState {
	state := graph.NewGraphState()

	state.Nodes["urn:moos:kernel:hp-z440.primary"] = graph.Node{
		URN:        "urn:moos:kernel:hp-z440.primary",
		TypeID:     "kernel",
		Properties: map[string]graph.Property{"name": {Value: "primary", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:kernel:hp-z440.menno"] = graph.Node{
		URN:        "urn:moos:kernel:hp-z440.menno",
		TypeID:     "kernel",
		Properties: map[string]graph.Property{"name": {Value: "menno", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:kernel:hp-z440.lola"] = graph.Node{
		URN:        "urn:moos:kernel:hp-z440.lola",
		TypeID:     "kernel",
		Properties: map[string]graph.Property{"name": {Value: "lola", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:kernel:hp-z440.moos"] = graph.Node{
		URN:        "urn:moos:kernel:hp-z440.moos",
		TypeID:     "kernel",
		Properties: map[string]graph.Property{"name": {Value: "moos", Mutability: "immutable"}},
	}

	state.Nodes["urn:moos:user:menno"] = graph.Node{
		URN:        "urn:moos:user:menno",
		TypeID:     "user",
		Properties: map[string]graph.Property{"name": {Value: "menno", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:user:lola"] = graph.Node{
		URN:        "urn:moos:user:lola",
		TypeID:     "user",
		Properties: map[string]graph.Property{"name": {Value: "lola", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:workstation:hp-z440"] = graph.Node{
		URN:        "urn:moos:workstation:hp-z440",
		TypeID:     "workstation",
		Properties: map[string]graph.Property{"hostname": {Value: "hp-z440", Mutability: "immutable"}},
	}
	state.Nodes["urn:moos:feed:arxiv.cs-ai"] = graph.Node{
		URN:        "urn:moos:feed:arxiv.cs-ai",
		TypeID:     "source_feed",
		Properties: map[string]graph.Property{"name": {Value: "arxiv", Mutability: "immutable"}},
	}

	state.Relations["urn:moos:rel:menno.owns.ws"] = graph.Relation{
		URN:             "urn:moos:rel:menno.owns.ws",
		RewriteCategory: graph.WF01,
		SrcURN:          "urn:moos:user:menno",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-z440",
		TgtPort:         "child",
	}
	state.Relations["urn:moos:rel:lola.owns.ws"] = graph.Relation{
		URN:             "urn:moos:rel:lola.owns.ws",
		RewriteCategory: graph.WF01,
		SrcURN:          "urn:moos:user:lola",
		SrcPort:         "owns",
		TgtURN:          "urn:moos:workstation:hp-z440",
		TgtPort:         "child",
	}

	return state
}
