package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"moos/kernel/internal/graph"
)

// muxWithTCone returns a ServeMux with just the /t-cone route bound.
func muxWithTCone(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /t-cone", s.handleGetTCone)
	return mux
}

// stateForTCone builds a state with:
//   - user:sam (superadmin via WF02)
//   - session:sam.hp-laptop (seat_role=occupier) --has-occupant--> user:sam
//   - session:sam.idle       (seat_role=observer, no occupant)
//   - program:sam.prog-a with an open t_hook (fires_at=200)
//   - program:sam.prog-b with a t_hook (fires_at=500) that doesn't fire yet
//   - governance_proposal:sam.propose-x (admin-scope type)
//   - t_hook on governance_proposal (fires_at=100) — only admin should see
func stateForTCone() graph.GraphState {
	return graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:user:sam": {
				URN: "urn:moos:user:sam", TypeID: "user",
				Properties: map[string]graph.Property{"name": {Value: "sam", Mutability: "immutable"}},
			},
			"urn:moos:role:superadmin": {
				URN: "urn:moos:role:superadmin", TypeID: "role",
				Properties: map[string]graph.Property{"name": {Value: "superadmin", Mutability: "immutable"}},
			},
			"urn:moos:session:sam.hp-laptop": {
				URN: "urn:moos:session:sam.hp-laptop", TypeID: "session",
				Properties: map[string]graph.Property{"seat_role": {Value: "occupier", Mutability: "mutable"}},
			},
			"urn:moos:session:sam.idle": {
				URN: "urn:moos:session:sam.idle", TypeID: "session",
				Properties: map[string]graph.Property{"seat_role": {Value: "observer", Mutability: "mutable"}},
			},
			"urn:moos:program:sam.prog-a": {
				URN: "urn:moos:program:sam.prog-a", TypeID: "program",
				Properties: map[string]graph.Property{"status": {Value: "active", Mutability: "mutable"}},
			},
			"urn:moos:program:sam.prog-b": {
				URN: "urn:moos:program:sam.prog-b", TypeID: "program",
				Properties: map[string]graph.Property{"status": {Value: "draft", Mutability: "mutable"}},
			},
			"urn:moos:governance_proposal:sam.propose-x": {
				URN: "urn:moos:governance_proposal:sam.propose-x", TypeID: "governance_proposal",
				Properties: map[string]graph.Property{"title": {Value: "Admin-scoped proposal", Mutability: "immutable"}},
			},
			"urn:moos:t_hook:sam.hook-a": {
				URN: "urn:moos:t_hook:sam.hook-a", TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog-a", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 200}, Mutability: "immutable"},
				},
			},
			"urn:moos:t_hook:sam.hook-b": {
				URN: "urn:moos:t_hook:sam.hook-b", TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog-b", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 500}, Mutability: "immutable"},
				},
			},
			"urn:moos:t_hook:sam.hook-admin": {
				URN: "urn:moos:t_hook:sam.hook-admin", TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:governance_proposal:sam.propose-x", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 100}, Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:sam.hp-laptop.has-occupant.sam": {
				URN:             "urn:moos:rel:sam.hp-laptop.has-occupant.sam",
				RewriteCategory: "WF19",
				SrcURN:          "urn:moos:session:sam.hp-laptop",
				SrcPort:         "has-occupant",
				TgtURN:          "urn:moos:user:sam",
				TgtPort:         "is-occupant-of",
			},
			"urn:moos:rel:sam.governs.superadmin": {
				URN:             "urn:moos:rel:sam.governs.superadmin",
				RewriteCategory: "WF02",
				SrcURN:          "urn:moos:user:sam",
				SrcPort:         "governs",
				TgtURN:          "urn:moos:role:superadmin",
				TgtPort:         "governed-by",
			},
		},
	}
}

// TestTCone_MissingSessionParam — no session query param returns 400.
func TestTCone_MissingSessionParam(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET", "/t-cone?at=220", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing session param, got %d", rec.Code)
	}
}

// TestTCone_NotASession — session URN points at a non-session node → 400.
func TestTCone_NotASession(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET", "/t-cone?session=urn:moos:user:sam&at=220", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-session URN, got %d", rec.Code)
	}
}

// TestTCone_NotLive — session with seat_role=observer returns 400.
// t-cone projects the occupant's view; only occupier/delegate seats have one.
func TestTCone_NotLive(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET", "/t-cone?session=urn:moos:session:sam.idle&at=220", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for observer-seat session, got %d", rec.Code)
	}
}

// TestTCone_UnknownSession — nonexistent session URN → 404.
func TestTCone_UnknownSession(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET", "/t-cone?session=urn:moos:session:sam.nonexistent&at=220", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d", rec.Code)
	}
}

// TestTCone_BasicFiring — admin occupant at T=250 should see both prog-a (hook fires)
// and the admin-scope governance_proposal (hook fires at T>=100). prog-b's hook at 500 doesn't fire.
func TestTCone_BasicFiring(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET",
		"/t-cone?session=urn:moos:session:sam.hp-laptop&at=250", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Metadata
	if resp["session"] != "urn:moos:session:sam.hp-laptop" {
		t.Errorf("session mismatch, got %v", resp["session"])
	}
	if resp["occupant"] != "urn:moos:user:sam" {
		t.Errorf("occupant mismatch, got %v", resp["occupant"])
	}
	if tT, _ := resp["t"].(float64); tT != 250 {
		t.Errorf("t mismatch, got %v", resp["t"])
	}

	nodes, ok := resp["nodes"].([]any)
	if !ok {
		t.Fatalf("nodes field missing or wrong type: %T", resp["nodes"])
	}
	// Admin occupant should see both prog-a (hook fires=true) and governance_proposal
	// (hook fires=true, admin-scoped).
	urns := map[string]bool{}
	for _, n := range nodes {
		m := n.(map[string]any)
		urns[m["urn"].(string)] = true
	}
	if !urns["urn:moos:program:sam.prog-a"] {
		t.Errorf("expected prog-a in cone (hook fires at T=250)")
	}
	if !urns["urn:moos:governance_proposal:sam.propose-x"] {
		t.Errorf("expected governance_proposal in cone (admin occupant, hook fires)")
	}
	if urns["urn:moos:program:sam.prog-b"] {
		t.Errorf("prog-b should NOT be in cone (hook fires_at=500 > T=250)")
	}
}

// TestTCone_NonAdminFiltersAdminScope — an occupant WITHOUT superadmin does
// not see admin-scoped node types (governance_proposal, role, capability,
// ontology_publication), even when their hooks fire.
func TestTCone_NonAdminFiltersAdminScope(t *testing.T) {
	state := stateForTCone()
	// Remove the WF02 superadmin relation so the occupant is non-admin.
	delete(state.Relations, "urn:moos:rel:sam.governs.superadmin")

	srv := serverWithState(state)
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET",
		"/t-cone?session=urn:moos:session:sam.hp-laptop&at=250", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	nodes, _ := resp["nodes"].([]any)
	for _, n := range nodes {
		m := n.(map[string]any)
		if tid, _ := m["type_id"].(string); tid == "governance_proposal" {
			t.Errorf("non-admin occupant should NOT see governance_proposal; got %v", m["urn"])
		}
	}
}

// TestTCone_UnoccupiedSession — session exists, is live (seat_role=occupier),
// but has no has-occupant relation. Returns 200 with empty nodes rather than
// erroring (the cone is empty, not malformed).
func TestTCone_UnoccupiedSession(t *testing.T) {
	state := stateForTCone()
	// Remove the has-occupant relation. Session is live but orphaned.
	delete(state.Relations, "urn:moos:rel:sam.hp-laptop.has-occupant.sam")

	srv := serverWithState(state)
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET",
		"/t-cone?session=urn:moos:session:sam.hp-laptop&at=250", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for live-but-unoccupied session, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["occupant"] != nil && resp["occupant"] != "" {
		t.Errorf("expected nil/empty occupant, got %v", resp["occupant"])
	}
	nodes, _ := resp["nodes"].([]any)
	if len(nodes) != 0 {
		t.Errorf("expected empty cone for unoccupied session, got %d nodes", len(nodes))
	}
}

// TestTCone_ResponseShape — verify each emitted node carries urn, type_id,
// properties, and open_t_hooks array with each hook having urn + predicate_kind.
func TestTCone_ResponseShape(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET",
		"/t-cone?session=urn:moos:session:sam.hp-laptop&at=250", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	nodes, _ := resp["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("expected at least one node in cone")
	}

	for _, n := range nodes {
		m := n.(map[string]any)
		for _, f := range []string{"urn", "type_id", "properties", "open_t_hooks"} {
			if _, ok := m[f]; !ok {
				t.Errorf("node missing field %q: %v", f, m)
			}
		}
		hooks, _ := m["open_t_hooks"].([]any)
		if len(hooks) == 0 {
			t.Errorf("node %v has empty open_t_hooks — should have at least one fire", m["urn"])
			continue
		}
		for _, h := range hooks {
			hm := h.(map[string]any)
			if _, ok := hm["urn"]; !ok {
				t.Errorf("hook missing urn: %v", hm)
			}
			if _, ok := hm["predicate_kind"]; !ok {
				t.Errorf("hook missing predicate_kind: %v", hm)
			}
		}
	}
}

// TestTCone_DefaultAtT — omitting at query param defaults to currentTDay().
func TestTCone_DefaultAtT(t *testing.T) {
	srv := serverWithState(stateForTCone())
	mux := muxWithTCone(srv)

	req := httptest.NewRequest("GET",
		"/t-cone?session=urn:moos:session:sam.hp-laptop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without at, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["t"].(float64); !ok {
		t.Errorf("expected numeric t in response, got %T: %v", resp["t"], resp["t"])
	}
}
