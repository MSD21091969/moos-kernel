package kernel

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
	"moos/kernel/internal/operad"
)

const subscriberBufferSize = 64

// Runtime is the kernel — the effect layer wrapping the pure catamorphism.
// It holds the current graph state (derived), the append-only log (truth),
// the operad registry (grammar), and the subscriber map (pub/sub).
//
// state(t) = fold(log[0..t])  (CI-4)
//
// All writes go through Apply or ApplyProgram.
// Reads are lock-free via State() snapshot.
type Runtime struct {
	mu       sync.RWMutex
	state    graph.GraphState
	log      []graph.PersistedRewrite
	store    Store
	registry *operad.Registry

	subscriberMu sync.Mutex
	subscribers  map[string]chan graph.PersistedRewrite

	logSeq atomic.Int64
}

// NewRuntime creates a Runtime by replaying the full store into memory.
// All future Apply calls will be atomic: validate → fold → log → broadcast.
func NewRuntime(store Store, registry *operad.Registry) (*Runtime, error) {
	entries, err := store.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("kernel: read store: %w", err)
	}

	state, err := fold.Replay(entries)
	if err != nil {
		return nil, fmt.Errorf("kernel: replay: %w", err)
	}

	rt := &Runtime{
		state:       state,
		log:         entries,
		store:       store,
		registry:    registry,
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}
	if len(entries) > 0 {
		rt.logSeq.Store(entries[len(entries)-1].LogSeq)
	}
	return rt, nil
}

// Apply validates and applies one Envelope atomically.
// Order: operad validate → fold.Evaluate → lock → persist → broadcast.
func (rt *Runtime) Apply(env graph.Envelope) (graph.EvalResult, error) {
	// Operad validation (type system — read-lock not needed, registry is read-only)
	if err := rt.validate(env); err != nil {
		return graph.EvalResult{}, err
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	// For MUTATE, pass the current node to operad for authority check
	if env.RewriteType == graph.MUTATE {
		node, ok := rt.state.Nodes[env.TargetURN]
		if ok {
			if err := rt.registry.ValidateMUTATE(env, node); err != nil {
				return graph.EvalResult{}, err
			}
		}
	}

	next, result, err := fold.Evaluate(rt.state, env)
	if err != nil {
		return graph.EvalResult{}, err
	}

	seq := rt.logSeq.Add(1)
	persisted := graph.PersistedRewrite{
		Envelope:  env,
		AppliedAt: time.Now().UTC(),
		LogSeq:    seq,
	}
	if err := rt.store.Append([]graph.PersistedRewrite{persisted}); err != nil {
		return graph.EvalResult{}, fmt.Errorf("kernel: persist: %w", err)
	}

	rt.state = next
	rt.log = append(rt.log, persisted)
	rt.broadcast(persisted)

	return result, nil
}

// ApplyProgram applies a slice of Envelopes atomically.
// All or nothing: if any step fails, no state change and nothing is persisted.
func (rt *Runtime) ApplyProgram(envelopes []graph.Envelope) ([]graph.EvalResult, error) {
	for _, env := range envelopes {
		if err := rt.validate(env); err != nil {
			return nil, err
		}
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	nextState, results, err := fold.EvaluateProgram(rt.state, envelopes)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	persisted := make([]graph.PersistedRewrite, len(envelopes))
	for i, env := range envelopes {
		seq := rt.logSeq.Add(1)
		persisted[i] = graph.PersistedRewrite{
			Envelope:  env,
			AppliedAt: now.Add(time.Duration(i) * time.Nanosecond),
			LogSeq:    seq,
		}
	}

	if err := rt.store.Append(persisted); err != nil {
		return nil, fmt.Errorf("kernel: persist program: %w", err)
	}

	rt.state = nextState
	rt.log = append(rt.log, persisted...)
	for _, p := range persisted {
		rt.broadcast(p)
	}
	return results, nil
}

// SeedIfAbsent is Apply that silently absorbs ErrNodeExists and ErrRelationExists.
// Used for idempotent bootstrap seeding of infrastructure nodes.
func (rt *Runtime) SeedIfAbsent(env graph.Envelope) error {
	_, err := rt.Apply(env)
	if err == nil {
		return nil
	}
	if errors.Is(err, fold.ErrNodeExists) || errors.Is(err, fold.ErrRelationExists) {
		return nil
	}
	return err
}

// --- InspectKernel ---

func (rt *Runtime) State() graph.GraphState {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.state.Clone()
}

func (rt *Runtime) Node(urn graph.URN) (graph.Node, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	n, ok := rt.state.Nodes[urn]
	return n, ok
}

func (rt *Runtime) Nodes() []graph.Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	nodes := make([]graph.Node, 0, len(rt.state.Nodes))
	for _, n := range rt.state.Nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

func (rt *Runtime) Relation(urn graph.URN) (graph.Relation, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	r, ok := rt.state.Relations[urn]
	return r, ok
}

func (rt *Runtime) Relations() []graph.Relation {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	rels := make([]graph.Relation, 0, len(rt.state.Relations))
	for _, r := range rt.state.Relations {
		rels = append(rels, r)
	}
	return rels
}

func (rt *Runtime) RelationsSrc(urn graph.URN) []graph.Relation {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out []graph.Relation
	for _, r := range rt.state.Relations {
		if r.SrcURN == urn {
			out = append(out, r)
		}
	}
	return out
}

func (rt *Runtime) RelationsTgt(urn graph.URN) []graph.Relation {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out []graph.Relation
	for _, r := range rt.state.Relations {
		if r.TgtURN == urn {
			out = append(out, r)
		}
	}
	return out
}

func (rt *Runtime) Log() []graph.PersistedRewrite {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	cp := make([]graph.PersistedRewrite, len(rt.log))
	copy(cp, rt.log)
	return cp
}

func (rt *Runtime) LogLen() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.log)
}

// --- ObservableKernel ---

func (rt *Runtime) Subscribe(id string) <-chan graph.PersistedRewrite {
	rt.subscriberMu.Lock()
	defer rt.subscriberMu.Unlock()
	ch := make(chan graph.PersistedRewrite, subscriberBufferSize)
	rt.subscribers[id] = ch
	return ch
}

func (rt *Runtime) Unsubscribe(id string) {
	rt.subscriberMu.Lock()
	defer rt.subscriberMu.Unlock()
	if ch, ok := rt.subscribers[id]; ok {
		close(ch)
		delete(rt.subscribers, id)
	}
}

func (rt *Runtime) broadcast(pr graph.PersistedRewrite) {
	rt.subscriberMu.Lock()
	defer rt.subscriberMu.Unlock()
	for _, ch := range rt.subscribers {
		select {
		case ch <- pr:
		default:
			// Slow subscriber: drop rather than block the kernel
		}
	}
}

// validate dispatches to the appropriate operad validator by rewrite type.
func (rt *Runtime) validate(env graph.Envelope) error {
	if rt.registry == nil {
		return nil
	}
	switch env.RewriteType {
	case graph.ADD:
		return rt.registry.ValidateADD(env)
	case graph.LINK:
		return rt.registry.ValidateLINK(env)
	case graph.UNLINK:
		return rt.registry.ValidateUNLINK(env)
	case graph.MUTATE:
		// MUTATE needs the current node — done inline in Apply with lock held
		return nil
	}
	return nil
}
