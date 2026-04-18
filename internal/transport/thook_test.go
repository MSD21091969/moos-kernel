package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"moos/kernel/internal/graph"
)

// fakeInspect is a minimal kernel.InspectKernel that backs the handler tests
// with a hand-built state. Only State() and Node() are exercised by the
// /t-hook/evaluate handler; the rest stub out for interface compliance.
type fakeInspect struct {
	state graph.GraphState
}

func (f *fakeInspect) State() graph.GraphState { return f.state }
func (f *fakeInspect) Node(urn graph.URN) (graph.Node, bool) {
	n, ok := f.state.Nodes[urn]
	return n, ok
}
func (f *fakeInspect) Nodes() []graph.Node {
	out := make([]graph.Node, 0, len(f.state.Nodes))
	for _, n := range f.state.Nodes {
		out = append(out, n)
	}
	return out
}
func (f *fakeInspect) Relation(urn graph.URN) (graph.Relation, bool) {
	r, ok := f.state.Relations[urn]
	return r, ok
}
func (f *fakeInspect) Relations() []graph.Relation          { return nil }
func (f *fakeInspect) RelationsSrc(graph.URN) []graph.Relation { return nil }
func (f *fakeInspect) RelationsTgt(graph.URN) []graph.Relation { return nil }
func (f *fakeInspect) Log() []graph.PersistedRewrite        { return nil }
func (f *fakeInspect) LogLen() int                         { return 0 }

// stateWithStartableHook builds a small state with t187-kernel-proper and a
// v310-delivery.startable t_hook whose predicate mirrors the one ADDed in
// T=168 round 8.
func stateWithStartableHook(kernelProperStatus string) graph.GraphState {
	return graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.t187-kernel-proper": {
				URN:    "urn:moos:program:sam.t187-kernel-proper",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: kernelProperStatus, Mutability: "mutable"},
				},
			},
			"urn:moos:t_hook:sam.v310-delivery.startable": {
				URN:    "urn:moos:t_hook:sam.v310-delivery.startable",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {
						Value:      "urn:moos:program:sam.v310-delivery",
						Mutability: "immutable",
					},
					"predicate": {
						Value: map[string]any{
							"kind": "all_of",
							"predicates": []any{
								map[string]any{"kind": "fires_at", "t": 220},
								map[string]any{
									"kind":  "after_urn",
									"urn":   "urn:moos:program:sam.t187-kernel-proper",
									"prop":  "status",
									"value": "completed",
								},
							},
						},
						Mutability: "immutable",
					},
					"react_template": {
						Value: map[string]any{
							"rewrite_type": "MUTATE",
							"target_urn":   "urn:moos:program:sam.v310-delivery",
							"field":        "status",
							"new_value":    "startable",
						},
						Mutability: "mutable",
					},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
}

// serverWithState wires a fake InspectKernel into a Server without hitting
// the Runtime constructor. Only the fields the handler actually reads are
// populated; the rest stay nil and that's fine because the handler only
// calls s.inspect.State().
func serverWithState(state graph.GraphState) *Server {
	return &Server{inspect: &fakeInspect{state: state}}
}

// muxWithTHook returns a ServeMux with just the t-hook evaluate route — no
// CORS middleware. The handler under test is directly routable this way.
func muxWithTHook(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /t-hook/evaluate/{urn}", s.handleGetTHookEvaluate)
	return mux
}

// TestTHookEvaluate_FiresAtFuture — at T=168 the compound predicate is false
// (both the time gate and the status gate are unsatisfied).
func TestTHookEvaluate_FiresAtFuture(t *testing.T) {
	state := stateWithStartableHook("active")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET",
		"/t-hook/evaluate/urn:moos:t_hook:sam.v310-delivery.startable?at=168", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if fires, _ := resp["fires"].(bool); fires {
		t.Errorf("expected fires=false at T=168 with kernel-proper active; got %v", resp)
	}
	// JSON round-trip turns at_t into float64.
	if atT, _ := resp["at_t"].(float64); atT != 168 {
		t.Errorf("expected at_t=168, got %v", resp["at_t"])
	}
	if resp["owner_urn"] != "urn:moos:program:sam.v310-delivery" {
		t.Errorf("expected owner_urn in response, got %v", resp["owner_urn"])
	}
	if resp["predicate"] == nil {
		t.Error("expected predicate in response")
	}
	if resp["react_template"] == nil {
		t.Error("expected react_template in response")
	}
}

// TestTHookEvaluate_FiresWhenReady — at T=220 with kernel-proper completed,
// both gates pass and fires=true.
func TestTHookEvaluate_FiresWhenReady(t *testing.T) {
	state := stateWithStartableHook("completed")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET",
		"/t-hook/evaluate/urn:moos:t_hook:sam.v310-delivery.startable?at=220", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if fires, _ := resp["fires"].(bool); !fires {
		t.Errorf("expected fires=true at T=220 with kernel-proper completed; got %v", resp)
	}
}

// TestTHookEvaluate_NotFound — missing URN returns 404.
func TestTHookEvaluate_NotFound(t *testing.T) {
	state := stateWithStartableHook("active")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET", "/t-hook/evaluate/urn:moos:t_hook:sam.nonexistent?at=200", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestTHookEvaluate_WrongType — pointing the endpoint at a non-t_hook URN
// returns 400 with a descriptive error.
func TestTHookEvaluate_WrongType(t *testing.T) {
	state := stateWithStartableHook("active")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	// Point at a program node, not a t_hook.
	req := httptest.NewRequest("GET", "/t-hook/evaluate/urn:moos:program:sam.t187-kernel-proper?at=200", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-t_hook, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTHookEvaluate_NoPredicate — a t_hook missing the predicate property
// returns 422 (nothing to evaluate).
func TestTHookEvaluate_NoPredicate(t *testing.T) {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:t_hook:sam.bare": {
				URN:    "urn:moos:t_hook:sam.bare",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.t187-kernel-proper", Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET", "/t-hook/evaluate/urn:moos:t_hook:sam.bare?at=200", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for t_hook without predicate, got %d", rec.Code)
	}
}

// TestTHookEvaluate_InvalidAtParam — non-integer at= returns 400.
func TestTHookEvaluate_InvalidAtParam(t *testing.T) {
	state := stateWithStartableHook("active")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET",
		"/t-hook/evaluate/urn:moos:t_hook:sam.v310-delivery.startable?at=not-a-number", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid at param, got %d", rec.Code)
	}
}

// TestTHookEvaluate_DefaultAtT — omitting at= defaults to currentTDay().
// We don't assert a specific T value (it's wall-clock), just that the
// handler accepts the request and returns a numeric at_t in the response.
func TestTHookEvaluate_DefaultAtT(t *testing.T) {
	state := stateWithStartableHook("active")
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET",
		"/t-hook/evaluate/urn:moos:t_hook:sam.v310-delivery.startable", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without at param, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["at_t"].(float64); !ok {
		t.Errorf("expected numeric at_t in response, got %T: %v", resp["at_t"], resp["at_t"])
	}
}
