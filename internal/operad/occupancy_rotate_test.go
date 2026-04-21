package operad

import (
	"strings"
	"testing"

	"moos/kernel/internal/graph"
)

// ------------------------------------------------------------------
// §M19 RotateSessionOccupant — atomic UNLINK+LINK program emitter
// ------------------------------------------------------------------

// stateForRotation extends stateWithOccupancy (from occupancy_test.go) with
// an agent node, giving us a second principal to rotate TO.
func stateForRotation() graph.GraphState {
	st := stateWithOccupancy()
	st.Nodes["urn:moos:agent:claude-code.hp-z440"] = graph.Node{
		URN:    "urn:moos:agent:claude-code.hp-z440",
		TypeID: "agent",
		Properties: map[string]graph.Property{
			"name": {Value: "claude-code.hp-z440", Mutability: "immutable"},
		},
	}
	return st
}

func TestRotateSessionOccupant_OccupiedSession_EmitsUnlinkPlusLink(t *testing.T) {
	state := stateForRotation()

	res, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:agent:claude-code.hp-z440",
		"urn:moos:kernel:hp-z440.primary",
		"urn:moos:rel:sam.hp-laptop.has-occupant.claude-z440",
	)
	if err != nil {
		t.Fatalf("unexpected rotation error: %v", err)
	}
	if got, want := len(res.Envelopes), 2; got != want {
		t.Fatalf("envelopes count = %d, want %d", got, want)
	}

	unlink, link := res.Envelopes[0], res.Envelopes[1]
	if unlink.RewriteType != graph.UNLINK {
		t.Errorf("envelope[0].RewriteType = %s, want UNLINK", unlink.RewriteType)
	}
	if unlink.RelationURN != "urn:moos:rel:sam.hp-laptop.has-occupant.user-sam" {
		t.Errorf("unlink.RelationURN = %s, want the prior has-occupant relation", unlink.RelationURN)
	}
	if unlink.RewriteCategory != graph.WF19 {
		t.Errorf("unlink.RewriteCategory = %s, want WF19", unlink.RewriteCategory)
	}

	if link.RewriteType != graph.LINK {
		t.Errorf("envelope[1].RewriteType = %s, want LINK", link.RewriteType)
	}
	if link.SrcURN != "urn:moos:session:sam.hp-laptop" {
		t.Errorf("link.SrcURN = %s, want session", link.SrcURN)
	}
	if link.SrcPort != hasOccupantSrcPort || link.TgtPort != isOccupantOfTgtPort {
		t.Errorf("link port pair = (%s, %s), want canonical (%s, %s)",
			link.SrcPort, link.TgtPort, hasOccupantSrcPort, isOccupantOfTgtPort)
	}
	if link.TgtURN != "urn:moos:agent:claude-code.hp-z440" {
		t.Errorf("link.TgtURN = %s, want new occupant", link.TgtURN)
	}
	if link.RewriteCategory != graph.WF19 {
		t.Errorf("link.RewriteCategory = %s, want WF19", link.RewriteCategory)
	}

	if res.PriorOccupant != "urn:moos:user:sam" {
		t.Errorf("res.PriorOccupant = %s, want urn:moos:user:sam", res.PriorOccupant)
	}
	if res.PriorRelationURN != "urn:moos:rel:sam.hp-laptop.has-occupant.user-sam" {
		t.Errorf("res.PriorRelationURN = %s, want the prior has-occupant URN", res.PriorRelationURN)
	}
}

func TestRotateSessionOccupant_UnoccupiedSession_EmitsOnlyLink(t *testing.T) {
	state := stateForRotation()

	res, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.idle", // no has-occupant in fixture
		"urn:moos:user:sam",
		"urn:moos:kernel:hp-z440.primary",
		"urn:moos:rel:sam.idle.has-occupant.user-sam",
	)
	if err != nil {
		t.Fatalf("unexpected rotation error on initial seat: %v", err)
	}
	if got, want := len(res.Envelopes), 1; got != want {
		t.Fatalf("envelopes count = %d, want %d (initial seat = LINK only)", got, want)
	}
	if res.Envelopes[0].RewriteType != graph.LINK {
		t.Errorf("envelope[0].RewriteType = %s, want LINK", res.Envelopes[0].RewriteType)
	}
	if res.PriorOccupant != "" || res.PriorRelationURN != "" {
		t.Errorf("expected zero prior state for unoccupied rotation, got prior=%s rel=%s",
			res.PriorOccupant, res.PriorRelationURN)
	}
}

func TestRotateSessionOccupant_NoOp_RejectedExplicitly(t *testing.T) {
	state := stateForRotation()
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:user:sam", // same as current occupant — no-op
		"urn:moos:kernel:hp-z440.primary",
		"urn:moos:rel:sam.hp-laptop.has-occupant.user-sam-v2",
	)
	if err == nil {
		t.Fatalf("expected no-op rotation to error; got nil")
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Errorf("expected error to mention 'no-op'; got %q", err.Error())
	}
}

func TestRotateSessionOccupant_EmptySessionURN(t *testing.T) {
	state := stateForRotation()
	_, err := RotateSessionOccupant(state, "", "urn:moos:user:sam", "actor", "urn:moos:rel:x")
	if err == nil {
		t.Fatalf("expected error for empty sessionURN; got nil")
	}
	if !strings.Contains(err.Error(), "sessionURN is empty") {
		t.Errorf("error message = %q; expected to mention 'sessionURN is empty'", err.Error())
	}
}

func TestRotateSessionOccupant_EmptyNewOccupantURN(t *testing.T) {
	state := stateForRotation()
	_, err := RotateSessionOccupant(state, "urn:moos:session:sam.hp-laptop", "", "actor", "urn:moos:rel:x")
	if err == nil {
		t.Fatalf("expected error for empty newOccupantURN; got nil")
	}
	if !strings.Contains(err.Error(), "newOccupantURN is empty") {
		t.Errorf("error message = %q; expected to mention 'newOccupantURN is empty'", err.Error())
	}
}

func TestRotateSessionOccupant_SessionMissing(t *testing.T) {
	state := stateForRotation()
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:does-not-exist",
		"urn:moos:user:sam",
		"actor",
		"urn:moos:rel:x",
	)
	if err == nil || !strings.Contains(err.Error(), "session node not found") {
		t.Fatalf("expected 'session node not found' error; got %v", err)
	}
}

func TestRotateSessionOccupant_SessionURNNotASession(t *testing.T) {
	state := stateForRotation()
	// Pass a user URN where a session URN is expected.
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:user:sam",
		"urn:moos:agent:claude-code.hp-z440",
		"actor",
		"urn:moos:rel:x",
	)
	if err == nil || !strings.Contains(err.Error(), "is not a session") {
		t.Fatalf("expected 'is not a session' error; got %v", err)
	}
}

func TestRotateSessionOccupant_NewOccupantNotPrincipal(t *testing.T) {
	state := stateForRotation()
	// Add a non-principal node (kernel is not user|agent).
	state.Nodes["urn:moos:kernel:hp-z440.primary"] = graph.Node{
		URN:    "urn:moos:kernel:hp-z440.primary",
		TypeID: "kernel",
		Properties: map[string]graph.Property{
			"name": {Value: "hp-z440.primary", Mutability: "immutable"},
		},
	}
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:kernel:hp-z440.primary", // wrong type
		"actor",
		"urn:moos:rel:x",
	)
	if err == nil || !strings.Contains(err.Error(), "must be user|agent") {
		t.Fatalf("expected principal-type rejection; got %v", err)
	}
}

func TestRotateSessionOccupant_DoctrineViolation_MultipleHasOccupant(t *testing.T) {
	// Construct a bad state: two has-occupant relations on the same session.
	// §M19 declares at-most-one; the helper refuses to guess which to rotate.
	state := stateForRotation()
	state.Relations["urn:moos:rel:bad.second-has-occupant"] = graph.Relation{
		URN:             "urn:moos:rel:bad.second-has-occupant",
		RewriteCategory: graph.WF19,
		SrcURN:          "urn:moos:session:sam.hp-laptop",
		SrcPort:         "has-occupant",
		TgtURN:          "urn:moos:agent:claude-code.hp-z440",
		TgtPort:         "is-occupant-of",
	}
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:agent:claude-code.hp-z440",
		"actor",
		"urn:moos:rel:new",
	)
	if err == nil || !strings.Contains(err.Error(), "at-most-one violation") {
		t.Fatalf("expected at-most-one violation error; got %v", err)
	}
}

func TestRotateSessionOccupant_NewRelationURNCollidesWithPrior(t *testing.T) {
	state := stateForRotation()
	// Reuse the existing has-occupant relation URN for the new LINK — caller bug.
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:agent:claude-code.hp-z440",
		"actor",
		"urn:moos:rel:sam.hp-laptop.has-occupant.user-sam", // same as prior
	)
	if err == nil || !strings.Contains(err.Error(), "already exists in state") {
		t.Fatalf("expected collision rejection (already-exists message); got %v", err)
	}
}

// TestRotateSessionOccupant_NewRelationURNCollidesWithUnrelatedRelation is the
// tightening Gemini flagged: a newRelationURN that collides with ANY existing
// relation — not just the prior has-occupant — must be rejected. Catches bugs
// where a caller reuses a URN that happens to be taken by an unrelated LINK
// (which would otherwise surface as ErrRelationExists at LINK apply time with
// a less informative message).
func TestRotateSessionOccupant_NewRelationURNCollidesWithUnrelatedRelation(t *testing.T) {
	state := stateForRotation()
	// Add an unrelated relation (not has-occupant on this session) whose URN
	// the caller accidentally tries to reuse.
	state.Relations["urn:moos:rel:unrelated.something"] = graph.Relation{
		URN:             "urn:moos:rel:unrelated.something",
		RewriteCategory: graph.WF02,
		SrcURN:          "urn:moos:user:sam",
		SrcPort:         "governs",
		TgtURN:          "urn:moos:agent:claude-code.hp-z440",
		TgtPort:         "governed-by",
	}
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:agent:claude-code.hp-z440",
		"actor",
		"urn:moos:rel:unrelated.something", // collides with unrelated relation
	)
	if err == nil || !strings.Contains(err.Error(), "already exists in state") {
		t.Fatalf("expected collision rejection against unrelated relation; got %v", err)
	}
}

// TestRotateSessionOccupant_EmptyActor closes the Gemini + Copilot concern
// that the helper previously let an empty actor through — fold would have
// rejected the emitted envelopes with ErrMissingActor at submission time, a
// confusing failure path. The helper now fails early.
func TestRotateSessionOccupant_EmptyActor(t *testing.T) {
	state := stateForRotation()
	_, err := RotateSessionOccupant(
		state,
		"urn:moos:session:sam.hp-laptop",
		"urn:moos:agent:claude-code.hp-z440",
		"", // empty actor — now rejected up front
		"urn:moos:rel:new",
	)
	if err == nil || !strings.Contains(err.Error(), "actor is empty") {
		t.Fatalf("expected 'actor is empty' rejection; got %v", err)
	}
}
