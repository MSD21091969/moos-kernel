package operad

import (
	"testing"

	"moos/kernel/internal/graph"
)

// TestPortColor_HasOccupant — v3.10 WF19 ports must map to ColorWorkflow so
// operad LINK validation accepts session --has-occupant/is-occupant-of--> principal.
func TestPortColor_HasOccupant(t *testing.T) {
	// portColorFromName is unexported; test via the public resolvePortColors
	// path which is how ValidateLINK reaches it.
	r := &Registry{}
	src, tgt, err := r.resolvePortColors("has-occupant", "is-occupant-of")
	if err != nil {
		t.Fatalf("resolvePortColors(has-occupant, is-occupant-of): %v", err)
	}
	if src != graph.ColorWorkflow {
		t.Errorf("has-occupant: expected ColorWorkflow, got %s", src)
	}
	if tgt != graph.ColorWorkflow {
		t.Errorf("is-occupant-of: expected ColorWorkflow, got %s", tgt)
	}
}

// --------------------------------------------------------------------
// §M19 ResolveSessionOccupant — walk has-occupant from a session node
// --------------------------------------------------------------------

// stateWithOccupancy builds a state with:
//   session sam.hp-laptop --has-occupant--> user:sam
//   (separately) session sam.idle (no has-occupant relation)
func stateWithOccupancy() graph.GraphState {
	return graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:user:sam": {
				URN:    "urn:moos:user:sam",
				TypeID: "user",
				Properties: map[string]graph.Property{
					"name": {Value: "sam", Mutability: "immutable"},
				},
			},
			"urn:moos:session:sam.hp-laptop": {
				URN:    "urn:moos:session:sam.hp-laptop",
				TypeID: "session",
				Properties: map[string]graph.Property{
					"seat_role": {Value: "occupier", Mutability: "mutable"},
				},
			},
			"urn:moos:session:sam.idle": {
				URN:    "urn:moos:session:sam.idle",
				TypeID: "session",
				Properties: map[string]graph.Property{
					"seat_role": {Value: "observer", Mutability: "mutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:sam.hp-laptop.has-occupant.user-sam": {
				URN:             "urn:moos:rel:sam.hp-laptop.has-occupant.user-sam",
				RewriteCategory: "WF19",
				SrcURN:          "urn:moos:session:sam.hp-laptop",
				SrcPort:         "has-occupant",
				TgtURN:          "urn:moos:user:sam",
				TgtPort:         "is-occupant-of",
			},
		},
	}
}

func TestResolveSessionOccupant_Hit(t *testing.T) {
	state := stateWithOccupancy()
	occ, ok := ResolveSessionOccupant(state, "urn:moos:session:sam.hp-laptop")
	if !ok {
		t.Fatalf("expected occupant for session with has-occupant relation, got ok=false")
	}
	if occ != "urn:moos:user:sam" {
		t.Errorf("expected occupant=urn:moos:user:sam, got %s", occ)
	}
}

func TestResolveSessionOccupant_MissRelation(t *testing.T) {
	state := stateWithOccupancy()
	occ, ok := ResolveSessionOccupant(state, "urn:moos:session:sam.idle")
	if ok {
		t.Errorf("expected no occupant for session without has-occupant relation, got %s", occ)
	}
}

func TestResolveSessionOccupant_MissSession(t *testing.T) {
	state := stateWithOccupancy()
	_, ok := ResolveSessionOccupant(state, "urn:moos:session:sam.nonexistent")
	if ok {
		t.Errorf("expected no occupant for nonexistent session")
	}
}

// --------------------------------------------------------------------
// §M12 CheckAdminCapability — actor → session → occupant → WF02 role:superadmin
// --------------------------------------------------------------------

// stateWithAdminChain builds a state where sam IS a superadmin via WF02
// and hp-laptop session IS occupied by sam.
func stateWithAdminChain() graph.GraphState {
	state := stateWithOccupancy()
	// Add the superadmin role and the WF02 governance LINK.
	state.Nodes["urn:moos:role:superadmin"] = graph.Node{
		URN:    "urn:moos:role:superadmin",
		TypeID: "role",
		Properties: map[string]graph.Property{
			"name": {Value: "superadmin", Mutability: "immutable"},
		},
	}
	state.Relations["urn:moos:rel:sam.governs.superadmin"] = graph.Relation{
		URN:             "urn:moos:rel:sam.governs.superadmin",
		RewriteCategory: "WF02",
		SrcURN:          "urn:moos:user:sam",
		SrcPort:         "governs",
		TgtURN:          "urn:moos:role:superadmin",
		TgtPort:         "governed-by",
	}
	return state
}

func TestCheckAdminCapability_SessionActor(t *testing.T) {
	state := stateWithAdminChain()
	// Actor is the session — admin check walks session → occupant → WF02.
	if !CheckAdminCapability(state, "urn:moos:session:sam.hp-laptop") {
		t.Errorf("expected session with superadmin-occupant to pass admin check")
	}
}

func TestCheckAdminCapability_UserActor(t *testing.T) {
	state := stateWithAdminChain()
	// Actor is a user directly — still walks WF02.
	if !CheckAdminCapability(state, "urn:moos:user:sam") {
		t.Errorf("expected user with superadmin WF02 to pass admin check")
	}
}

func TestCheckAdminCapability_NoOccupant(t *testing.T) {
	state := stateWithAdminChain()
	// Actor is an unoccupied session — admin check must fail closed.
	if CheckAdminCapability(state, "urn:moos:session:sam.idle") {
		t.Errorf("expected unoccupied session to fail admin check (fail-closed)")
	}
}

func TestCheckAdminCapability_NoRoleLink(t *testing.T) {
	state := stateWithOccupancy() // no superadmin role / WF02 in this state
	if CheckAdminCapability(state, "urn:moos:session:sam.hp-laptop") {
		t.Errorf("expected admin check to fail when WF02 superadmin chain is missing")
	}
}

func TestCheckAdminCapability_UnknownActor(t *testing.T) {
	state := stateWithAdminChain()
	// Actor refers to a node that doesn't exist — fail closed.
	if CheckAdminCapability(state, "urn:moos:user:nobody") {
		t.Errorf("expected unknown actor to fail admin check")
	}
}

// TestCheckAdminCapability_EmptyActor — actor URN is empty; must fail closed,
// not panic or pass.
func TestCheckAdminCapability_EmptyActor(t *testing.T) {
	state := stateWithAdminChain()
	if CheckAdminCapability(state, "") {
		t.Errorf("expected empty actor URN to fail admin check")
	}
}
