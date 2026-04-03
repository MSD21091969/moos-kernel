package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/kernel"
	"moos/kernel/internal/operad"
)

// Server is the HTTP adapter layer. Transport is NOT part of the graph.
// The kernel normalizes all inbound data into rewrites.
// Transport receives requests, converts them to Envelopes, calls Apply,
// and returns results. It has no business logic.
type Server struct {
	inspect  kernel.InspectKernel
	write    kernel.WriteKernel
	observe  kernel.ObservableKernel
	registry *operad.Registry
	tDay     int
}

func NewServer(rt *kernel.Runtime, registry *operad.Registry, tDay int) *Server {
	return &Server{inspect: rt, write: rt, observe: rt, registry: registry, tDay: tDay}
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

	return mux
}

// --- Health ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"log_len": s.inspect.LogLen(),
		"t_day":   s.tDay,
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

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(msg)})
}
