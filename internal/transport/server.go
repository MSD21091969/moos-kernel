package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

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
}

// tDay0 is T=0 as UTC: 2025-11-01 00:00 CEST = 2025-10-31 23:00 UTC.
var tDay0 = time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC)

func currentTDay() int { return int(time.Now().UTC().Sub(tDay0).Hours() / 24) }

func NewServer(rt *kernel.Runtime, registry *operad.Registry, _ int) *Server {
	return &Server{rt: rt, inspect: rt, write: rt, observe: rt, registry: registry}
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

	return mux
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

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(msg)})
}
