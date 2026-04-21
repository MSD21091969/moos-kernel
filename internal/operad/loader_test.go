package operad

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadRegistry_Version verifies the top-level "version" field of
// ontology.json is captured on the Registry so /healthz (and any future
// consumer) can report runtime ontology version without grepping for
// feature-flag markers. See ffs0#33 comment thread (claude-z440 T=171
// state-readback: "runtime version marker not exposed at /operad/node-types").
func TestLoadRegistry_Version(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ontology.json")
	// Minimal ontology shape — just enough structure to parse.
	body := `{
		"$schema": "https://moos.local/ontology.schema.json",
		"version": "3.12.0",
		"types": {"s2_infrastructure": [], "s1_grammar": [], "interaction_nodes": []},
		"rewrite_categories": [],
		"port_color_compatibility": {"matrix": {}}
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed ontology: %v", err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if got, want := reg.Version, "3.12.0"; got != want {
		t.Errorf("registry.Version = %q, want %q", got, want)
	}
}

// TestEmptyRegistry_VersionBlank confirms an empty registry reports an empty
// version string (not a panic) — the /healthz handler relies on this to
// short-circuit cleanly when the kernel runs without an --ontology flag.
func TestEmptyRegistry_VersionBlank(t *testing.T) {
	reg := EmptyRegistry()
	if reg.Version != "" {
		t.Errorf("EmptyRegistry().Version = %q, want empty string", reg.Version)
	}
}

// TestLoadRegistry_EmptyPath confirms passing an empty path yields an empty
// registry with an empty version string — matching the "no ontology loaded"
// fallback in LoadRegistry.
func TestLoadRegistry_EmptyPath(t *testing.T) {
	reg, err := LoadRegistry("")
	if err != nil {
		t.Fatalf("LoadRegistry(\"\"): %v", err)
	}
	if reg.Version != "" {
		t.Errorf("LoadRegistry(\"\").Version = %q, want empty string", reg.Version)
	}
}
