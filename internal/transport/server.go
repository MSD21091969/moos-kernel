package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
	"moos/kernel/internal/kernel"
	"moos/kernel/internal/operad"
)

// Server is the HTTP adapter layer. Transport is NOT part of the graph.
// The kernel normalizes all inbound data into rewrites.
// Transport receives requests, converts them to Envelopes, calls Apply,
// and returns results. It has no business logic.
type Server struct {
	rt       *kernel.Runtime
	inspect  kernel.InspectKernel
	write    kernel.WriteKernel
	observe  kernel.ObservableKernel
	registry *operad.Registry

	// altSvc, if non-empty, is emitted as the Alt-Svc response header to
	// advertise an HTTP/3 endpoint. Set via SetAltSvc from main.go once the
	// QUIC listener's actual UDP port is known. Empty means no Alt-Svc
	// header — the server is TCP-only and shouldn't advertise h3.
	altSvc string
}

// tDay0 is T=0 as UTC: 2025-11-01 00:00 CEST = 2025-10-31 23:00 UTC.
var tDay0 = time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC)

func currentTDay() int { return int(time.Now().UTC().Sub(tDay0).Hours() / 24) }

func NewServer(rt *kernel.Runtime, registry *operad.Registry, _ int) *Server {
	return &Server{rt: rt, inspect: rt, write: rt, observe: rt, registry: registry}
}

// SetAltSvc records the Alt-Svc header value to advertise on every response.
// Call from main.go after the QUIC listener is started, passing the actual
// configured UDP address. An empty string disables the header.
//
// This is not concurrency-safe post-startup: call it once before Handler()
// is attached to an http.Server, or before the first request is served.
func (s *Server) SetAltSvc(v string) { s.altSvc = v }

// corsMiddleware adds CORS headers to every response and handles OPTIONS
// preflight. The Alt-Svc header is only emitted when s.altSvc is non-empty
// (set by SetAltSvc once the QUIC listener's actual port is known), so
// we never advertise an HTTP/3 endpoint that doesn't exist.
//
// SECURITY NOTE (PR #8 review): Allow-Origin: * is intentional for local
// dev / LAN deployments where any browser page needs to reach the kernel
// during exploration. For production deployments that expose the kernel
// beyond the host, wrap with a stricter CORS policy or set up an
// authenticating reverse proxy.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if s.altSvc != "" {
			w.Header().Set("Alt-Svc", s.altSvc)
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	mux.HandleFunc("GET /state/nodes", s.handleGetNodes)
	mux.HandleFunc("GET /state/nodes/{urn}", s.handleGetNode)
	mux.HandleFunc("GET /state/relations", s.handleGetRelations)
	mux.HandleFunc("GET /state/relations/src/{urn}", s.handleGetRelationsSrc)
	mux.HandleFunc("GET /state/relations/tgt/{urn}", s.handleGetRelationsTgt)

	mux.HandleFunc("GET /log", s.handleGetLog)
	mux.HandleFunc("GET /log/stream", s.handleLogStream)

	mux.HandleFunc("GET /fold", s.handleGetFold)
	mux.HandleFunc("GET /fold/stream", s.handleFoldStream)

	mux.HandleFunc("POST /rewrites", s.handlePostRewrite)
	mux.HandleFunc("POST /programs", s.handlePostProgram)

	mux.HandleFunc("GET /operad/node-types", s.handleGetNodeTypes)
	mux.HandleFunc("GET /operad/rewrite-categories", s.handleGetRewriteCategories)
	mux.HandleFunc("GET /operad/port-colors", s.handleGetPortColors)

	mux.HandleFunc("GET /hdc/similarity-matrix", s.handleGetHDCSimilarityMatrix)
	mux.HandleFunc("GET /hdc/eigenvalues", s.handleGetHDCEigenvalues)
	mux.HandleFunc("GET /hdc/fiedler", s.handleGetHDCFiedler)
	mux.HandleFunc("GET /hdc/type-coherence", s.handleGetHDCTypeCoherence)
	mux.HandleFunc("GET /hdc/type-expression", s.handleGetHDCTypeExpression)
	mux.HandleFunc("GET /hdc/fiber", s.handleGetHDCFiber)
	mux.HandleFunc("GET /hdc/federation", s.handleGetHDCFederation)
	mux.HandleFunc("GET /hdc/fiber-assignment", s.handleGetHDCFiberAssignment)
	mux.HandleFunc("GET /hdc/fiber-distribution", s.handleGetHDCFiberDistribution)
	mux.HandleFunc("GET /hdc/crosswalk", s.handleGetHDCCrosswalk)
	mux.HandleFunc("GET /hdc/crosswalk/composition-check", s.handleGetHDCCrosswalkComposition)
	mux.HandleFunc("GET /hdc/crosswalk/suggestions", s.handleGetHDCCrosswalkSuggestions)
	mux.HandleFunc("GET /hdc/classification-space", s.handleGetHDCClassificationSpace)

	// Twin-kernel endpoints (M9 — adjoint sync protocol)
	mux.HandleFunc("POST /twin/ingest", s.handleTwinIngest)
	mux.HandleFunc("GET /twin/status", s.handleTwinStatus)

	return s.corsMiddleware(mux)
}

// --- Health ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"log_len": s.inspect.LogLen(),
		"t_day":   currentTDay(),
	})
}

// --- State ---

func (s *Server) handleGetNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inspect.Nodes())
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	urn := graph.URN(r.PathValue("urn"))
	node, ok := s.inspect.Node(urn)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node not found: %s", urn))
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleGetRelations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inspect.Relations())
}

func (s *Server) handleGetRelationsSrc(w http.ResponseWriter, r *http.Request) {
	urn := graph.URN(r.PathValue("urn"))
	writeJSON(w, http.StatusOK, s.inspect.RelationsSrc(urn))
}

func (s *Server) handleGetRelationsTgt(w http.ResponseWriter, r *http.Request) {
	urn := graph.URN(r.PathValue("urn"))
	writeJSON(w, http.StatusOK, s.inspect.RelationsTgt(urn))
}

// --- Log ---

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inspect.Log())
}

// handleLogStream streams new rewrites via SSE (Server-Sent Events).
// Clients receive one "data: <json>\n\n" event per persisted rewrite.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id := fmt.Sprintf("sse-%d", time.Now().UnixNano())
	ch := s.observe.Subscribe(id)
	defer s.observe.Unsubscribe(id)

	for {
		select {
		case pr, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(pr)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// --- Fold ---

// handleGetFold returns the graph state projected at log position `to`.
// GET /fold?to=<n>  — state at time n (replay log[0..n])
// GET /fold         — current state (equivalent to full log replay)
// This is the M3 catamorphism exposed as an HTTP observable.
func (s *Server) handleGetFold(w http.ResponseWriter, r *http.Request) {
	log := s.inspect.Log()

	to := len(log)
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			if v < 0 {
				v = 0
			}
			if v > len(log) {
				v = len(log)
			}
			to = v
		}
	}

	state, err := fold.Replay(log[:to])
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fold: "+err.Error())
		return
	}

	nodes := make([]graph.Node, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		nodes = append(nodes, n)
	}
	rels := make([]graph.Relation, 0, len(state.Relations))
	for _, rel := range state.Relations {
		rels = append(rels, rel)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"t":         to,
		"log_len":   len(log),
		"nodes":     nodes,
		"relations": rels,
	})
}

// handleFoldStream streams state evolution via SSE with proper event types.
//
// On connect: emits one `event: snapshot` carrying the full state at time T.
// Thereafter: emits one `event: rewrite` per newly-applied rewrite.
//
// Clients can reconstruct state(t+k) = fold(snapshot + rewrites[0..k]).
// Browser EventSource dispatches to named listeners: `es.addEventListener("snapshot", ...)`
// and `es.addEventListener("rewrite", ...)`. The wire format now uses SSE
// `event:` framing rather than an embedded JSON field so standard EventSource
// clients work out of the box (PR #8 review, Copilot).
func (s *Server) handleFoldStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe first so we don't miss rewrites between snapshot and stream start.
	id := fmt.Sprintf("fold-sse-%d", time.Now().UnixNano())
	ch := s.observe.Subscribe(id)
	defer s.observe.Unsubscribe(id)

	// Send current state snapshot as the initial "snapshot" event.
	log := s.inspect.Log()
	if state, err := fold.Replay(log); err == nil {
		nodes := make([]graph.Node, 0, len(state.Nodes))
		for _, n := range state.Nodes {
			nodes = append(nodes, n)
		}
		rels := make([]graph.Relation, 0, len(state.Relations))
		for _, rel := range state.Relations {
			rels = append(rels, rel)
		}
		snap := map[string]any{
			"t":         len(log),
			"nodes":     nodes,
			"relations": rels,
		}
		if data, err := json.Marshal(snap); err == nil {
			fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}

	// Stream subsequent rewrites as "rewrite" events.
	for {
		select {
		case pr, open := <-ch:
			if !open {
				return
			}
			evt := map[string]any{
				"log_seq": pr.LogSeq,
				"rewrite": pr,
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: rewrite\ndata: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// --- Rewrites ---

func (s *Server) handlePostRewrite(w http.ResponseWriter, r *http.Request) {
	var env graph.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := s.write.Apply(env)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePostProgram(w http.ResponseWriter, r *http.Request) {
	var envelopes []graph.Envelope
	if err := json.NewDecoder(r.Body).Decode(&envelopes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	results, err := s.write.ApplyProgram(envelopes)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// --- Operad ---

func (s *Server) handleGetNodeTypes(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, s.registry.NodeTypes)
}

func (s *Server) handleGetRewriteCategories(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, s.registry.RewriteCategories)
}

func (s *Server) handleGetPortColors(w http.ResponseWriter, r *http.Request) {
	if s.registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, s.registry.PortColorMatrix)
}

// --- HDC ---

func (s *Server) handleGetHDCSimilarityMatrix(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	_, _, _, entries := hdc.SimilarityMatrix(state, nil)
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleGetHDCEigenvalues(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	_, _, similarity, _ := hdc.SimilarityMatrix(state, nil)
	laplacian := hdc.Laplacian(similarity)
	values, _ := hdc.EigenDecompositionSymmetric(laplacian)

	top := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed > 0 {
			top = parsed
		}
	}
	if top > len(values) {
		top = len(values)
	}
	writeJSON(w, http.StatusOK, values[:top])
}

func (s *Server) handleGetHDCFiedler(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	urns, _, similarity, _ := hdc.SimilarityMatrix(state, nil)
	laplacian := hdc.Laplacian(similarity)
	values, vectors := hdc.EigenDecompositionSymmetric(laplacian)
	writeJSON(w, http.StatusOK, hdc.FiedlerPartition(urns, values, vectors))
}

func (s *Server) handleGetHDCTypeCoherence(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	writeJSON(w, http.StatusOK, hdc.TypeCoherence(state, nil))
}

func (s *Server) handleGetHDCTypeExpression(w http.ResponseWriter, r *http.Request) {
	rows := s.rt.HDCTypeExpressions()
	if len(rows) == 0 {
		state := s.inspect.State()
		rows = hdc.TypeExpressions(state, nil)
	}

	threshold := -1.0
	if raw := strings.TrimSpace(r.URL.Query().Get("threshold")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid threshold")
			return
		}
		threshold = parsed
	}

	if threshold >= 0 {
		filtered := make([]hdc.TypeExpressionEntry, 0, len(rows))
		for _, row := range rows {
			if row.Drift > threshold {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("sort")), "drift") {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Drift == rows[j].Drift {
				return rows[i].URN < rows[j].URN
			}
			return rows[i].Drift > rows[j].Drift
		})
	}

	top := -1
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid top")
			return
		}
		top = parsed
	}
	if top > 0 && top < len(rows) {
		rows = rows[:top]
	}

	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleGetHDCFiber(w http.ResponseWriter, r *http.Request) {
	kernelURN := graph.URN(strings.TrimSpace(r.URL.Query().Get("kernel")))
	if kernelURN == "" {
		writeError(w, http.StatusBadRequest, "missing kernel query parameter")
		return
	}

	state := s.inspect.State()
	node, ok := state.Nodes[kernelURN]
	if !ok || node.TypeID != "kernel" {
		writeError(w, http.StatusNotFound, "kernel not found")
		return
	}

	writeJSON(w, http.StatusOK, hdc.FiberVectorForKernel(state, kernelURN, nil))
}

func (s *Server) handleGetHDCFederation(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	writeJSON(w, http.StatusOK, hdc.FederationVector(state, nil))
}

func (s *Server) handleGetHDCFiberAssignment(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	rows := hdc.FiberAssignments(state, nil)

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("only_misplaced")), "true") {
		filtered := make([]hdc.FiberAssignmentEntry, 0, len(rows))
		for _, row := range rows {
			if row.CurrentKernel != row.OptimalKernel {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("sort")), "distance") {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Distance == rows[j].Distance {
				return rows[i].URN < rows[j].URN
			}
			return rows[i].Distance > rows[j].Distance
		})
	}

	top := -1
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid top")
			return
		}
		top = parsed
	}
	if top > 0 && top < len(rows) {
		rows = rows[:top]
	}

	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleGetHDCFiberDistribution(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	writeJSON(w, http.StatusOK, hdc.FiberDistribution(state))
}

func (s *Server) handleGetHDCCrosswalk(w http.ResponseWriter, r *http.Request) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "missing from/to query parameters")
		return
	}

	state := s.inspect.State()
	res, ok := hdc.ComputeCrosswalk(state, from, to, nil)
	if !ok {
		writeError(w, http.StatusNotFound, "scheme not found")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetHDCCrosswalkComposition(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	rows := hdc.CrosswalkCompositionChecks(state, nil)

	top := -1
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid top")
			return
		}
		top = parsed
	}
	if top > 0 && top < len(rows) {
		rows = rows[:top]
	}

	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleGetHDCCrosswalkSuggestions(w http.ResponseWriter, r *http.Request) {
	threshold := 0.7
	if raw := strings.TrimSpace(r.URL.Query().Get("threshold")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid threshold")
			return
		}
		threshold = parsed
	}

	state := s.inspect.State()
	writeJSON(w, http.StatusOK, hdc.CrosswalkSuggestions(state, nil, threshold))
}

func (s *Server) handleGetHDCClassificationSpace(w http.ResponseWriter, r *http.Request) {
	state := s.inspect.State()
	writeJSON(w, http.StatusOK, hdc.ClassificationSpace(state, nil))
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(msg)})
}
