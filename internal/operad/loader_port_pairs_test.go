package operad

import (
	"os"
	"path/filepath"
	"testing"

	"moos/kernel/internal/graph"
)

// TestLoadRegistry_AdditionalPortPairs verifies the loader captures the
// additional_port_pairs field introduced into rewrite_categories at v3.10.
// Pre-PR-1 the field was ignored; this test pins the new behavior so any
// regression (typo'd tag, dropped loop) fails loudly.
func TestLoadRegistry_AdditionalPortPairs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ontology.json")
	body := `{
		"version": "3.12.0",
		"types": {"s2_infrastructure": [], "s1_grammar": [], "interaction_nodes": []},
		"rewrite_categories": [
			{
				"id": "WF19",
				"name": "Session governance",
				"allowed_rewrites": ["LINK", "UNLINK", "MUTATE"],
				"src_types": ["session"],
				"tgt_types": ["kernel"],
				"src_port": "opens-on",
				"tgt_port": "occupied-by",
				"additional_port_pairs": [
					{
						"src_port": "has-occupant",
						"tgt_port": "is-occupant-of",
						"src_types": ["session"],
						"tgt_types": ["user", "agent"],
						"added_in_version": "3.10.0",
						"promotes_fragment": "urn:moos:grammar_fragment:d19-1-session-has-occupant",
						"description": "Occupancy topology"
					},
					{
						"src_port": "pins-urn",
						"tgt_port": "pinned-by-session",
						"src_types": ["session"],
						"tgt_types": ["*"],
						"added_in_version": "3.12.0"
					}
				],
				"authority": "kernel",
				"sync_mode": "local-only"
			}
		],
		"port_color_compatibility": {"matrix": {}}
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed ontology: %v", err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}

	spec, ok := reg.RewriteCategories[graph.WF19]
	if !ok {
		t.Fatal("WF19 missing from registry")
	}
	if got, want := len(spec.AdditionalPortPairs), 2; got != want {
		t.Fatalf("WF19 additional_port_pairs count = %d, want %d", got, want)
	}

	// v3.10 pair — has-occupant / is-occupant-of
	p0 := spec.AdditionalPortPairs[0]
	if p0.SrcPort != "has-occupant" || p0.TgtPort != "is-occupant-of" {
		t.Errorf("pair[0] ports = (%s, %s), want (has-occupant, is-occupant-of)",
			p0.SrcPort, p0.TgtPort)
	}
	if p0.AddedInVersion != "3.10.0" {
		t.Errorf("pair[0].AddedInVersion = %q, want 3.10.0", p0.AddedInVersion)
	}
	if p0.PromotesFragment != "urn:moos:grammar_fragment:d19-1-session-has-occupant" {
		t.Errorf("pair[0].PromotesFragment = %q, want the d19-1 URN", p0.PromotesFragment)
	}
	if p0.Description == "" {
		t.Errorf("pair[0].Description should carry through; got empty")
	}
	if len(p0.SrcTypes) != 1 || p0.SrcTypes[0] != "session" {
		t.Errorf("pair[0].SrcTypes = %v, want [session]", p0.SrcTypes)
	}
	if len(p0.TgtTypes) != 2 {
		t.Errorf("pair[0].TgtTypes count = %d, want 2", len(p0.TgtTypes))
	}

	// v3.12 pair — pins-urn / pinned-by-session
	p1 := spec.AdditionalPortPairs[1]
	if p1.SrcPort != "pins-urn" || p1.TgtPort != "pinned-by-session" {
		t.Errorf("pair[1] ports = (%s, %s), want (pins-urn, pinned-by-session)",
			p1.SrcPort, p1.TgtPort)
	}
	if p1.AddedInVersion != "3.12.0" {
		t.Errorf("pair[1].AddedInVersion = %q, want 3.12.0", p1.AddedInVersion)
	}
}

// TestLoadRegistry_LoadsRealOntology_WF19Pairs is a belt-and-braces integration
// check against the actual ontology.json at ffs0/kb/superset/ontology.json,
// run when the file is present at its expected sibling path. The test
// auto-skips when the path isn't available (e.g. in a detached build where
// ffs0 is not a sibling), so it doesn't break isolated CI. When present, it
// guarantees the loader sees all WF19 additional_port_pairs declared in the
// canonical ontology.
func TestLoadRegistry_LoadsRealOntology_WF19Pairs(t *testing.T) {
	candidates := []string{
		"../../../ffs0/kb/superset/ontology.json",
		"../../../../ffs0/kb/superset/ontology.json",
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		t.Skip("ontology.json not found at expected sibling paths; skipping integration check")
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry(%q): %v", path, err)
	}
	spec, ok := reg.RewriteCategories[graph.WF19]
	if !ok {
		t.Fatal("WF19 missing from real ontology registry")
	}
	// v3.12.0 declares 4 additional pairs on WF19: has-occupant, pins-urn,
	// filtered-by, mounts-tool. A count below that signals the loader dropped
	// entries.
	if len(spec.AdditionalPortPairs) < 4 {
		t.Errorf("expected >=4 additional_port_pairs on WF19 (v3.12+); got %d", len(spec.AdditionalPortPairs))
	}

	// has-occupant pair must be present — it's the §M19 topology backbone.
	found := false
	for _, p := range spec.AdditionalPortPairs {
		if p.SrcPort == "has-occupant" && p.TgtPort == "is-occupant-of" {
			found = true
			break
		}
	}
	if !found {
		t.Error("has-occupant/is-occupant-of pair missing from real ontology WF19")
	}
}
