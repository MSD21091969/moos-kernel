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
				RewriteCategory: graph.WF19,
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
		RewriteCategory: graph.WF02,
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

// -----------------------------------------------------------------------------
// Tightening regressions from PR #13 review (Copilot)
// -----------------------------------------------------------------------------

// TestResolveSessionOccupant_RejectsNonSession — ResolveSessionOccupant
// must fail-closed when the URN points at a node that exists but isn't
// type_id=="session". Prior behaviour silently walked has-occupant off any
// node.
func TestResolveSessionOccupant_RejectsNonSession(t *testing.T) {
	state := stateWithOccupancy()
	// user:sam exists; pretend someone passed its URN by mistake.
	_, ok := ResolveSessionOccupant(state, "urn:moos:user:sam")
	if ok {
		t.Errorf("expected ResolveSessionOccupant to reject non-session URN")
	}
}

// TestResolveSessionOccupant_RequiresBothPorts — the WF19 port pair must
// match on both sides. A relation with SrcPort=has-occupant but a different
// TgtPort is ignored.
func TestResolveSessionOccupant_RequiresBothPorts(t *testing.T) {
	state := stateWithOccupancy()
	// Corrupt the TgtPort so the pair no longer matches on both sides.
	rel := state.Relations["urn:moos:rel:sam.hp-laptop.has-occupant.user-sam"]
	rel.TgtPort = "some-other-port"
	state.Relations["urn:moos:rel:sam.hp-laptop.has-occupant.user-sam"] = rel

	_, ok := ResolveSessionOccupant(state, "urn:moos:session:sam.hp-laptop")
	if ok {
		t.Errorf("expected ResolveSessionOccupant to require matching TgtPort")
	}
}

// TestResolveSessionOccupant_RejectsNonPrincipalTarget — the target of
// has-occupant must be a user or agent. Any other type_id fails closed.
func TestResolveSessionOccupant_RejectsNonPrincipalTarget(t *testing.T) {
	state := stateWithOccupancy()
	// Retarget has-occupant at a type_id that isn't user/agent.
	state.Nodes["urn:moos:program:sam.bogus"] = graph.Node{
		URN: "urn:moos:program:sam.bogus", TypeID: "program",
		Properties: map[string]graph.Property{"title": {Value: "not a principal", Mutability: "immutable"}},
	}
	state.Relations["rel-bogus"] = graph.Relation{
		URN:             "rel-bogus",
		RewriteCategory: graph.WF19,
		SrcURN:          "urn:moos:session:sam.idle",
		SrcPort:         "has-occupant",
		TgtURN:          "urn:moos:program:sam.bogus",
		TgtPort:         "is-occupant-of",
	}
	_, ok := ResolveSessionOccupant(state, "urn:moos:session:sam.idle")
	if ok {
		t.Errorf("expected ResolveSessionOccupant to reject program-typed target (not a user|agent)")
	}
}

// TestCheckAdminCapability_MissingSuperadminRoleFailsClosed — the admin
// check must fail closed when the superadmin role node itself is absent.
func TestCheckAdminCapability_MissingSuperadminRoleFailsClosed(t *testing.T) {
	state := stateWithAdminChain()
	delete(state.Nodes, "urn:moos:role:superadmin")
	// The WF02 relation still points at the (now missing) role. Admin check
	// must still fail — the link is stale.
	if CheckAdminCapability(state, "urn:moos:user:sam") {
		t.Errorf("expected admin check to fail when superadmin role node is absent")
	}
}

// TestCheckAdminCapability_RequiresWF02Category — the governance link must
// carry RewriteCategory=WF02. A link with the same ports but a different
// WF should be ignored.
func TestCheckAdminCapability_RequiresWF02Category(t *testing.T) {
	state := stateWithAdminChain()
	rel := state.Relations["urn:moos:rel:sam.governs.superadmin"]
	rel.RewriteCategory = graph.WF01 // wrong category
	state.Relations["urn:moos:rel:sam.governs.superadmin"] = rel

	if CheckAdminCapability(state, "urn:moos:user:sam") {
		t.Errorf("expected admin check to require WF02 rewrite_category")
	}
}

// TestCheckAdminCapability_RequiresGovernedByTgtPort — the WF02 link must
// close on both port names. A link with a different TgtPort is ignored.
func TestCheckAdminCapability_RequiresGovernedByTgtPort(t *testing.T) {
	state := stateWithAdminChain()
	rel := state.Relations["urn:moos:rel:sam.governs.superadmin"]
	rel.TgtPort = "bogus-port"
	state.Relations["urn:moos:rel:sam.governs.superadmin"] = rel

	if CheckAdminCapability(state, "urn:moos:user:sam") {
		t.Errorf("expected admin check to require TgtPort=governed-by")
	}
}

// TestCheckAdminCapability_RejectsNonPrincipalActor — if the actor URN
// points at a node that's neither session nor a recognised principal
// (user|agent), the check fails closed.
func TestCheckAdminCapability_RejectsNonPrincipalActor(t *testing.T) {
	state := stateWithAdminChain()
	// Add a governance_proposal node and point the WF02 relation at it.
	// The proposal exists, the role exists, but proposal isn't a principal.
	state.Nodes["urn:moos:governance_proposal:sam.x"] = graph.Node{
		URN: "urn:moos:governance_proposal:sam.x", TypeID: "governance_proposal",
		Properties: map[string]graph.Property{"title": {Value: "x", Mutability: "immutable"}},
	}
	state.Relations["rel-proposal-governs"] = graph.Relation{
		URN:             "rel-proposal-governs",
		RewriteCategory: graph.WF02,
		SrcURN:          "urn:moos:governance_proposal:sam.x",
		SrcPort:         "governs",
		TgtURN:          "urn:moos:role:superadmin",
		TgtPort:         "governed-by",
	}
	if CheckAdminCapability(state, "urn:moos:governance_proposal:sam.x") {
		t.Errorf("expected admin check to reject non-principal actor (governance_proposal)")
	}
}
