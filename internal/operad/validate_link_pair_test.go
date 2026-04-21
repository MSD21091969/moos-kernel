package operad

import (
	"strings"
	"testing"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
)

// buildTestRegistry returns a registry with WF19 plus a second WF that has
// no port spec at all — enough surface to exercise pair validation branches.
// The PortColorMatrix is seeded with workflow→workflow=allowed mirroring
// ontology.json so color-compatibility check does not intercept pair-validation
// assertions.
func buildTestRegistry() *Registry {
	reg := EmptyRegistry()

	// WF19 with its primary pair + the canonical v3.10 has-occupant pair.
	reg.RewriteCategories[graph.WF19] = RewriteCategorySpec{
		ID:              graph.WF19,
		Name:            "Session governance",
		AllowedRewrites: []graph.RewriteType{graph.LINK, graph.UNLINK, graph.MUTATE, graph.ADD},
		SrcPort:         "opens-on",
		TgtPort:         "occupied-by",
		AdditionalPortPairs: []AdditionalPortPair{
			{
				SrcPort:        "has-occupant",
				TgtPort:        "is-occupant-of",
				SrcTypes:       []graph.TypeID{"session"},
				TgtTypes:       []graph.TypeID{"user", "agent"},
				AddedInVersion: "3.10.0",
			},
			{
				SrcPort:        "pins-urn",
				TgtPort:        "pinned-by-session",
				SrcTypes:       []graph.TypeID{"session"},
				TgtTypes:       []graph.TypeID{"*"},
				AddedInVersion: "3.12.0",
			},
		},
		Authority: "kernel",
		SyncMode:  "local-only",
	}

	// WF99 simulating a degenerate / spec-absent WF — no primary, no additional.
	// ValidateLINK should remain permissive in that case for backward compat.
	reg.RewriteCategories["WF99"] = RewriteCategorySpec{
		ID:              "WF99",
		Name:            "Spec-absent category",
		AllowedRewrites: []graph.RewriteType{graph.LINK},
	}

	// Seed the port-color compatibility matrix with the workflow→workflow
	// entry required by the WF19 pairs under test. Matches the
	// `port_color_compatibility.matrix.workflow.workflow = true` line in
	// ffs0/kb/superset/ontology.json.
	reg.PortColorMatrix[graph.ColorWorkflow] = map[graph.PortColor]colorCompat{
		graph.ColorWorkflow: compatAllowed,
	}
	return reg
}

func TestValidateLINK_PrimaryPairAccepted(t *testing.T) {
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF19,
		SrcPort:         "opens-on",
		TgtPort:         "occupied-by",
		SrcURN:          "urn:moos:session:test",
		TgtURN:          "urn:moos:kernel:test",
	}
	if err := reg.ValidateLINK(env); err != nil {
		t.Fatalf("primary pair rejected: %v", err)
	}
}

func TestValidateLINK_AdditionalPairAccepted_HasOccupant(t *testing.T) {
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF19,
		SrcPort:         "has-occupant",
		TgtPort:         "is-occupant-of",
		SrcURN:          "urn:moos:session:test",
		TgtURN:          "urn:moos:agent:test",
	}
	if err := reg.ValidateLINK(env); err != nil {
		t.Fatalf("canonical has-occupant pair rejected: %v", err)
	}
}

func TestValidateLINK_AdditionalPairAccepted_PinsURN(t *testing.T) {
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF19,
		SrcPort:         "pins-urn",
		TgtPort:         "pinned-by-session",
		SrcURN:          "urn:moos:session:test",
		TgtURN:          "urn:moos:program:any",
	}
	if err := reg.ValidateLINK(env); err != nil {
		t.Fatalf("pins-urn pair rejected: %v", err)
	}
}

// TestValidateLINK_UndeclaredPairRejected is the key behavior change landed by
// PR 1. Pre-PR-1 the validator was permissive about any port pair not in the
// declared set (the src_port: "has-occupant" / tgt_port: "occupies" shape was
// silently accepted on Z440 kernel at log_seq=233). Post-PR-1 the validator
// rejects with a message that enumerates the declared alternatives.
func TestValidateLINK_UndeclaredPairRejected(t *testing.T) {
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF19,
		SrcPort:         "has-occupant",
		TgtPort:         "occupies", // typo — canonical is is-occupant-of
		SrcURN:          "urn:moos:session:test",
		TgtURN:          "urn:moos:agent:test",
	}
	err := reg.ValidateLINK(env)
	if err == nil {
		t.Fatalf("expected rejection of (has-occupant, occupies) under WF19; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "port pair") {
		t.Errorf("error does not mention 'port pair': %q", msg)
	}
	if !strings.Contains(msg, "has-occupant") || !strings.Contains(msg, "occupies") {
		t.Errorf("error should echo offending pair; got %q", msg)
	}
	if !strings.Contains(msg, "is-occupant-of") {
		t.Errorf("error should enumerate declared alternatives (expected is-occupant-of in message); got %q", msg)
	}
}

func TestValidateLINK_UndeclaredPrimaryWithoutAdditional(t *testing.T) {
	// Same surface but target pair is entirely off-ontology — still rejected.
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF19,
		SrcPort:         "invokes",
		TgtPort:         "invoked-by",
	}
	if err := reg.ValidateLINK(env); err == nil {
		t.Fatalf("expected rejection for undeclared pair (invokes, invoked-by) under WF19")
	}
}

// TestValidateLINK_SpecAbsentWFStaysPermissive confirms the legacy permissive
// behavior for WFs with no declared pairs (neither primary nor additional).
// The guard is important for backward compatibility with registries loaded
// from older ontology versions or test fixtures that omit port specs.
func TestValidateLINK_SpecAbsentWFStaysPermissive(t *testing.T) {
	reg := buildTestRegistry()
	env := graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: "WF99",
		SrcPort:         "whatever",
		TgtPort:         "unknown",
	}
	if err := reg.ValidateLINK(env); err != nil {
		t.Fatalf("spec-absent WF should stay permissive; got: %v", err)
	}
}

// TestReplay_GrandfathersNonCanonicalPortPair is the central doctrinal check
// for PR 1 backward compatibility. A persisted log containing a LINK with a
// non-canonical port pair (produced under pre-PR-1 permissive validation)
// must replay cleanly: fold is pure and does not re-validate. Tightening the
// validator affects new rewrites only. Historical truth in the log is
// preserved; state rebuilds identically.
//
// This mirrors the concrete artifact on Z440 at log_seq=233: AG's
// session:sam.moos-diary --has-occupant/occupies--> agent:antigravity.hp-z440
// LINK. PR 1 must not break replay of such entries.
func TestReplay_GrandfathersNonCanonicalPortPair(t *testing.T) {
	// Seed state with the two nodes the LINK references.
	log := []graph.PersistedRewrite{
		{
			LogSeq: 1,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam",
				NodeURN:     "urn:moos:session:test",
				TypeID:      "session",
				Properties: map[string]graph.Property{
					"started_at": {Value: "2026-04-21T00:00:00Z", Mutability: "immutable"},
				},
			},
		},
		{
			LogSeq: 2,
			Envelope: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       "urn:moos:user:sam",
				NodeURN:     "urn:moos:agent:test",
				TypeID:      "agent",
				Properties: map[string]graph.Property{
					"name": {Value: "test-agent", Mutability: "immutable"},
				},
			},
		},
		{
			LogSeq: 3,
			Envelope: graph.Envelope{
				RewriteType:     graph.LINK,
				Actor:           "urn:moos:kernel:test",
				RelationURN:     "urn:moos:rel:noncanonical",
				SrcURN:          "urn:moos:session:test",
				SrcPort:         "has-occupant",
				TgtURN:          "urn:moos:agent:test",
				TgtPort:         "occupies", // pre-PR-1 non-canonical shape
				RewriteCategory: graph.WF19,
			},
		},
	}

	state, err := fold.Replay(log)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}

	rel, ok := state.Relations["urn:moos:rel:noncanonical"]
	if !ok {
		t.Fatalf("non-canonical relation missing from replayed state")
	}
	if rel.SrcPort != "has-occupant" || rel.TgtPort != "occupies" {
		t.Errorf("replayed relation ports mutated: got (%s, %s), want (has-occupant, occupies)",
			rel.SrcPort, rel.TgtPort)
	}
	// Fold ignores operad — this test would pass even without PR 1 — but the
	// test documents the invariant so any future change that starts folding
	// through a validator surface here must confront the grandfathering rule.
}
