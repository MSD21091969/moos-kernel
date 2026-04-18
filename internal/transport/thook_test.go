package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"moos/kernel/internal/graph"
)

// fakeInspect is a minimal kernel.InspectKernel that backs the handler tests
// with a hand-built state. The /t-hook/evaluate handler exercises Node()
// first (to validate URN + type_id) and then State() (to evaluate
// predicates). The remaining methods stub out for interface compliance.
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
func (f *fakeInspect) Relations() []graph.Relation             { return nil }
func (f *fakeInspect) RelationsSrc(graph.URN) []graph.Relation { return nil }
func (f *fakeInspect) RelationsTgt(graph.URN) []graph.Relation { return nil }
func (f *fakeInspect) Log() []graph.PersistedRewrite           { return nil }
func (f *fakeInspect) LogLen() int                             { return 0 }

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

	fires, ok := resp["fires"].(bool)
	if !ok {
		t.Fatalf("expected fires to be a bool in response, got %T: %v", resp["fires"], resp["fires"])
	}
	if fires {
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

	fires, ok := resp["fires"].(bool)
	if !ok {
		t.Fatalf("expected fires to be a bool in response, got %T: %v", resp["fires"], resp["fires"])
	}
	if !fires {
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

// TestTHookEvaluate_NilPredicateValue — a t_hook whose predicate property
// exists but carries a nil Value (e.g. MUTATEd with new_value=null) must
// also return 422, not a spurious 200 with fires=false. Regression for PR
// #10 review (Copilot).
func TestTHookEvaluate_NilPredicateValue(t *testing.T) {
	state := graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:t_hook:sam.null-pred": {
				URN:    "urn:moos:t_hook:sam.null-pred",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.t187-kernel-proper", Mutability: "immutable"},
					"predicate": {Value: nil, Mutability: "mutable"}, // explicit nil
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
	srv := serverWithState(state)
	mux := muxWithTHook(srv)

	req := httptest.NewRequest("GET", "/t-hook/evaluate/urn:moos:t_hook:sam.null-pred?at=200", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for t_hook with nil predicate value, got %d: %s", rec.Code, rec.Body.String())
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

// ============================================================================
// M1 — POST /t-hook/evaluate batch endpoint tests
// ============================================================================

// stateWithMultipleHooks returns a state with two t_hooks and one program for
// batch-endpoint tests. hook-a fires at T=200, hook-b fires at T=300.
func stateWithMultipleHooks() graph.GraphState {
	return graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:program:sam.prog-x": {
				URN:    "urn:moos:program:sam.prog-x",
				TypeID: "program",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
				},
			},
			"urn:moos:t_hook:sam.hook-a": {
				URN:    "urn:moos:t_hook:sam.hook-a",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog-x", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 200}, Mutability: "immutable"},
					"react_template": {
						Value:      map[string]any{"rewrite_type": "MUTATE", "target_urn": "urn:moos:program:sam.prog-x", "field": "status", "new_value": "done"},
						Mutability: "mutable",
					},
				},
			},
			"urn:moos:t_hook:sam.hook-b": {
				URN:    "urn:moos:t_hook:sam.hook-b",
				TypeID: "t_hook",
				Properties: map[string]graph.Property{
					"owner_urn": {Value: "urn:moos:program:sam.prog-x", Mutability: "immutable"},
					"predicate": {Value: map[string]any{"kind": "fires_at", "t": 300}, Mutability: "immutable"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}
}

// muxWithBatchTHook returns a mux with just the POST /t-hook/evaluate route.
func muxWithBatchTHook(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /t-hook/evaluate", s.handleBatchTHookEvaluate)
	return mux
}

// postJSON is a small helper that POSTs a JSON-encoded body and returns the
// recorder. Content-Type is set; body encoding is json.Marshal of the value.
func postJSON(mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestTHookEvaluateBatch_EmptyURNs — empty array returns 200 with empty list.
func TestTHookEvaluateBatch_EmptyURNs(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{"urns": []string{}, "at": 220})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty urns, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d entries", len(resp))
	}
}

// TestTHookEvaluateBatch_InvalidJSON — malformed body returns 400.
func TestTHookEvaluateBatch_InvalidJSON(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	req := httptest.NewRequest("POST", "/t-hook/evaluate", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

// TestTHookEvaluateBatch_MissingBody — empty body returns 400.
func TestTHookEvaluateBatch_MissingBody(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	req := httptest.NewRequest("POST", "/t-hook/evaluate", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d", rec.Code)
	}
}

// TestTHookEvaluateBatch_TwoValidHooks — both evaluate correctly at T=250.
// hook-a fires (200<=250), hook-b doesn't (300>250).
func TestTHookEvaluateBatch_TwoValidHooks(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{
		"urns": []string{"urn:moos:t_hook:sam.hook-a", "urn:moos:t_hook:sam.hook-b"},
		"at":   250,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(resp), resp)
	}
	if resp[0]["urn"] != "urn:moos:t_hook:sam.hook-a" {
		t.Errorf("expected resp[0].urn=hook-a, got %v", resp[0]["urn"])
	}
	fires0, ok := resp[0]["fires"].(bool)
	if !ok {
		t.Fatalf("expected resp[0].fires to be bool, got %T", resp[0]["fires"])
	}
	if !fires0 {
		t.Errorf("expected hook-a fires=true at T=250 (fires_at=200); got false")
	}
	fires1, ok := resp[1]["fires"].(bool)
	if !ok {
		t.Fatalf("expected resp[1].fires to be bool, got %T", resp[1]["fires"])
	}
	if fires1 {
		t.Errorf("expected hook-b fires=false at T=250 (fires_at=300); got true")
	}
}

// TestTHookEvaluateBatch_MixedValidAndMissing — 3 URNs, 1 missing, 1 wrong-type.
// Batch endpoint returns per-entry errors inline so a single bad URN does not
// sink the whole request (O(1) client round-trip wins only if the call succeeds).
func TestTHookEvaluateBatch_MixedValidAndMissing(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{
		"urns": []string{
			"urn:moos:t_hook:sam.hook-a",      // valid t_hook
			"urn:moos:t_hook:sam.nonexistent", // missing
			"urn:moos:program:sam.prog-x",     // wrong type (program, not t_hook)
		},
		"at": 250,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (batch soft-errors per entry), got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp))
	}

	// resp[0] — valid hook, should have fires
	if _, ok := resp[0]["fires"].(bool); !ok {
		t.Errorf("resp[0] expected fires bool (valid hook); got %v", resp[0])
	}
	// resp[1] — missing node, should have error
	if resp[1]["error"] == nil {
		t.Errorf("resp[1] expected error for missing node; got %v", resp[1])
	}
	// resp[2] — wrong type, should have error
	if resp[2]["error"] == nil {
		t.Errorf("resp[2] expected error for non-t_hook; got %v", resp[2])
	}
}

// TestTHookEvaluateBatch_MissingAtDefault — at omitted defaults to currentTDay().
func TestTHookEvaluateBatch_MissingAtDefault(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{
		"urns": []string{"urn:moos:t_hook:sam.hook-a"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(resp))
	}
	if _, ok := resp[0]["at_t"].(float64); !ok {
		t.Errorf("expected at_t to be numeric (defaulted to currentTDay), got %T: %v", resp[0]["at_t"], resp[0]["at_t"])
	}
}

// TestTHookEvaluateBatch_ResponseFields — each successful entry carries the
// full set of fields (urn, at_t, fires, predicate, owner_urn, react_template
// when available) so callers can render without extra round-trips.
func TestTHookEvaluateBatch_ResponseFields(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{
		"urns": []string{"urn:moos:t_hook:sam.hook-a"},
		"at":   250,
	})

	var resp []map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(resp))
	}
	entry := resp[0]

	wantFields := []string{"urn", "at_t", "fires", "predicate", "owner_urn", "react_template"}
	for _, f := range wantFields {
		if _, ok := entry[f]; !ok {
			t.Errorf("expected response entry to have field %q; got keys %v", f, mapKeys(entry))
		}
	}
}

// TestTHookEvaluateBatch_OrderPreserved — response preserves request order
// even when some entries error. Important for clients that render side-by-side.
func TestTHookEvaluateBatch_OrderPreserved(t *testing.T) {
	srv := serverWithState(stateWithMultipleHooks())
	mux := muxWithBatchTHook(srv)

	urns := []string{
		"urn:moos:t_hook:sam.missing-1",
		"urn:moos:t_hook:sam.hook-a",
		"urn:moos:t_hook:sam.missing-2",
		"urn:moos:t_hook:sam.hook-b",
	}
	rec := postJSON(mux, "/t-hook/evaluate", map[string]any{"urns": urns, "at": 250})

	var resp []map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != len(urns) {
		t.Fatalf("expected %d entries, got %d", len(urns), len(resp))
	}
	for i, u := range urns {
		if resp[i]["urn"] != u {
			t.Errorf("order drift at index %d: want %q, got %q", i, u, resp[i]["urn"])
		}
	}
}

// mapKeys is a tiny debug helper for expected-field assertions.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
