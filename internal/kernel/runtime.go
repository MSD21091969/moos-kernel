package kernel

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"moos/kernel/internal/fold"
	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
	"moos/kernel/internal/operad"
	"moos/kernel/internal/reactive"
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
	hdcIndex *hdc.LiveIndex

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
		hdcIndex:    hdc.NewLiveIndex(0.3),
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}
	if len(entries) > 0 {
		rt.logSeq.Store(entries[len(entries)-1].LogSeq)
	}
	rt.hdcIndex.Recompute(state, nil)
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
	if env.RewriteType == graph.MUTATE && rt.registry != nil {
		node, ok := rt.state.Nodes[env.TargetURN]
		if ok {
			if err := rt.registry.ValidateMUTATE(env, node); err != nil {
				return graph.EvalResult{}, err
			}
			// Additive MUTATE: inject PropertySpec for fields declared in ontology but not yet on node.
			// This allows new optional properties (added in later ontology versions) to be set on existing nodes.
			env = rt.injectPropertySpec(env, node)
		}
	}

	next, result, err := fold.Evaluate(rt.state, env)
	if err != nil {
		return graph.EvalResult{}, err
	}

	seq := rt.logSeq.Add(1)
	now := time.Now().UTC()
	persisted := graph.PersistedRewrite{
		Envelope:  env,
		AppliedAt: now,
		Timestamp: now,
		LogSeq:    seq,
	}
	if err := rt.store.Append([]graph.PersistedRewrite{persisted}); err != nil {
		return graph.EvalResult{}, fmt.Errorf("kernel: persist: %w", err)
	}

	rt.state = next
	rt.log = append(rt.log, persisted)
	rt.broadcast(persisted)
	rt.runReactive(persisted)
	rt.runHDCIndexAndDriftLocked(env)

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

	// Inject PropertySpec for additive MUTATEs against the current state.
	// We walk the envelopes; the state evolves as we go (best-effort — use current state).
	injected := make([]graph.Envelope, len(envelopes))
	for i, env := range envelopes {
		if env.RewriteType == graph.MUTATE && rt.registry != nil {
			if node, ok := rt.state.Nodes[env.TargetURN]; ok {
				env = rt.injectPropertySpec(env, node)
			}
		}
		injected[i] = env
	}
	envelopes = injected

	nextState, results, err := fold.EvaluateProgram(rt.state, envelopes)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	persisted := make([]graph.PersistedRewrite, len(envelopes))
	for i, env := range envelopes {
		seq := rt.logSeq.Add(1)
		ts := now.Add(time.Duration(i) * time.Nanosecond)
		persisted[i] = graph.PersistedRewrite{
			Envelope:  env,
			AppliedAt: ts,
			Timestamp: ts,
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
	if len(envelopes) > 0 {
		rt.runHDCIndexAndDriftLocked(envelopes[len(envelopes)-1])
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

// HDCTypeExpressions returns a snapshot of the live in-memory type-expression index.
func (rt *Runtime) HDCTypeExpressions() []hdc.TypeExpressionEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.hdcIndex == nil {
		return nil
	}
	return rt.hdcIndex.Expressions()
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

// runReactive evaluates the Watch/React/Guard engine against a just-applied rewrite
// and applies any resulting proposals. Called with rt.mu already held.
// Proposals are applied at depth-1 only — reactive proposals do not trigger further reactions.
func (rt *Runtime) runReactive(trigger graph.PersistedRewrite) {
	// Snapshot state at the time of the trigger for engine evaluation.
	snapshot := rt.state
	eng := reactive.Engine{State: &snapshot}
	proposals := eng.Evaluate(trigger)

	for _, proposal := range proposals {
		rt.applyReactiveLocked(proposal)
	}
}

// applyReactiveLocked applies a single envelope while the write lock is already held.
// Used exclusively for reactive proposals — does NOT trigger further reactive evaluation.
func (rt *Runtime) applyReactiveLocked(env graph.Envelope) {
	// Operad validation (registry is read-only, no extra locking needed).
	if err := rt.validate(env); err != nil {
		return // drop invalid reactive proposals silently
	}
	if env.RewriteType == graph.MUTATE && rt.registry != nil {
		if node, ok := rt.state.Nodes[env.TargetURN]; ok {
			if err := rt.registry.ValidateMUTATE(env, node); err != nil {
				return
			}
		}
	}

	next, _, err := fold.Evaluate(rt.state, env)
	if err != nil {
		return // skip (e.g. ErrNodeExists for idempotent proposals)
	}

	seq := rt.logSeq.Add(1)
	now := time.Now().UTC()
	persisted := graph.PersistedRewrite{
		Envelope:  env,
		AppliedAt: now,
		Timestamp: now,
		LogSeq:    seq,
	}
	if err := rt.store.Append([]graph.PersistedRewrite{persisted}); err != nil {
		return // persist failure: drop, don't corrupt in-memory state
	}

	rt.state = next
	rt.log = append(rt.log, persisted)
	rt.broadcast(persisted)
}

// runHDCIndexAndDriftLocked updates the in-memory HDC index and emits type-drift claims.
// Called with rt.mu already held.
func (rt *Runtime) runHDCIndexAndDriftLocked(trigger graph.Envelope) {
	if rt.hdcIndex == nil {
		return
	}

	switch trigger.RewriteType {
	case graph.ADD, graph.LINK, graph.UNLINK, graph.MUTATE:
		// continue
	default:
		return
	}

	rt.hdcIndex.Recompute(rt.state, nil)
	drifted := rt.hdcIndex.Drifted()
	if len(drifted) == 0 {
		return
	}

	actor := rt.hdcActorURN(trigger.Actor)
	now := time.Now().UTC().Format(time.RFC3339)

	for _, row := range drifted {
		if strings.HasPrefix(row.URN.String(), "urn:moos:claim:type-drift.") {
			continue
		}
		if row.DeclaredType == "claim" {
			continue
		}

		hash := shortHash(row.URN.String())
		claimURN := graph.URN("urn:moos:claim:type-drift." + hash)
		if _, exists := rt.state.Nodes[claimURN]; exists {
			continue
		}

		confidence := row.Drift
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}

		addClaim := graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       actor,
			NodeURN:     claimURN,
			TypeID:      "claim",
			Properties: map[string]graph.Property{
				"text": {
					Value:      fmt.Sprintf("type drift detected for %s", row.URN),
					Mutability: "immutable",
				},
				"created_at": {
					Value:      now,
					Mutability: "immutable",
				},
				"source_ki_urn": {
					Value:      "urn:moos:ki:system.hdc-drift",
					Mutability: "immutable",
				},
				"confidence": {
					Value:          confidence,
					Mutability:     "mutable",
					AuthorityScope: "kernel",
				},
				"subject_urn": {
					Value:      row.URN.String(),
					Mutability: "immutable",
				},
				"declared_type": {
					Value:      string(row.DeclaredType),
					Mutability: "immutable",
				},
				"expressed_type": {
					Value:      string(row.ExpressedType),
					Mutability: "immutable",
				},
				"drift_score": {
					Value:          row.Drift,
					Mutability:     "mutable",
					AuthorityScope: "kernel",
				},
			},
		}
		rt.applyReactiveLocked(addClaim)

		relURN := graph.URN("urn:moos:rel:type-drift." + hash + ".annotation")
		if _, exists := rt.state.Relations[relURN]; exists {
			continue
		}

		linkClaim := graph.Envelope{
			RewriteType:     graph.LINK,
			RewriteCategory: graph.WF11,
			Actor:           actor,
			RelationURN:     relURN,
			SrcURN:          claimURN,
			SrcPort:         "tagged",
			TgtURN:          row.URN,
			TgtPort:         "tagged-in",
		}
		rt.applyReactiveLocked(linkClaim)
	}

	rt.hdcIndex.Recompute(rt.state, nil)
}

func (rt *Runtime) hdcActorURN(defaultActor graph.URN) graph.URN {
	if strings.HasPrefix(defaultActor.String(), "urn:moos:kernel:") {
		return defaultActor
	}

	kernels := make([]string, 0)
	for urn, node := range rt.state.Nodes {
		if node.TypeID == "kernel" {
			kernels = append(kernels, urn.String())
		}
	}
	if len(kernels) == 0 {
		return defaultActor
	}
	sort.Strings(kernels)
	for _, urn := range kernels {
		if strings.HasSuffix(urn, ".primary") {
			return graph.URN(urn)
		}
	}
	return graph.URN(kernels[0])
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:6])
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

// injectPropertySpec handles additive MUTATE: if the target field is not on the node but IS
// declared as mutable in the ontology type definition, inject the PropertySpec so fold can create it.
// This allows new optional properties (added in later ontology versions) to be set on existing nodes.
func (rt *Runtime) injectPropertySpec(env graph.Envelope, node graph.Node) graph.Envelope {
	if env.PropertySpec != nil {
		return env // already set (e.g. during replay)
	}
	if _, hasProp := node.Properties[env.Field]; hasProp {
		return env // field already on node — standard MUTATE path
	}
	typeSpec, hasType := rt.registry.NodeTypes[node.TypeID]
	if !hasType {
		return env
	}
	pspec, hasPspec := typeSpec.Properties[env.Field]
	if !hasPspec || pspec.Mutability != "mutable" {
		return env
	}
	env.PropertySpec = &graph.Property{
		Mutability:     pspec.Mutability,
		AuthorityScope: pspec.AuthorityScope,
		StratumOrigin:  2,
	}
	return env
}
