package graph

// GraphState is the current materialized state of the kernel graph.
// It is DERIVED — always reproducible from fold(log[0..t]).
// state(t) = fold(log[0..t])  (CI-4: same log → same state, always)
//
// Never treat GraphState as the source of truth. The append-only rewrite log is truth.
//
// In addition to Nodes and Relations, GraphState maintains three secondary
// indexes so hot paths (sweep, t-cone, occupancy walks) don't pay O(N) or
// O(R) scans on every call:
//
//   NodesByType      — for "give me all t_hooks" / "all governance_proposals"
//   RelationsBySrc   — for "what does session X point at?" (has-occupant walk)
//   RelationsByTgt   — for "who points at this role?" (governed-by walk)
//
// Indexes are derived from Nodes/Relations — they are NOT serialized (the
// JSON wire shape stays (nodes, relations) for backward compatibility with
// every /state, /fold, /twin payload). fold is responsible for maintaining
// them as rewrites apply; Rebuild reconstructs from scratch for cold start
// or replay.
type GraphState struct {
	Nodes     map[URN]Node     `json:"nodes"`
	Relations map[URN]Relation `json:"relations"`

	// Derived indexes (JSON-omitted). Nil until initialized — callers
	// that construct a GraphState manually should either use
	// NewGraphState, call Rebuild, or let fold populate them as rewrites
	// apply. Read paths MUST tolerate a nil index (treat as "empty").
	NodesByType    map[TypeID]map[URN]struct{} `json:"-"`
	RelationsBySrc map[URN]map[URN]struct{}    `json:"-"`
	RelationsByTgt map[URN]map[URN]struct{}    `json:"-"`
}

// NewGraphState returns a zero state with all maps (including the indexes)
// initialized. Callers should prefer this over the zero-value struct so
// that the indexes are ready before the first ADD/LINK.
func NewGraphState() GraphState {
	return GraphState{
		Nodes:          make(map[URN]Node),
		Relations:      make(map[URN]Relation),
		NodesByType:    make(map[TypeID]map[URN]struct{}),
		RelationsBySrc: make(map[URN]map[URN]struct{}),
		RelationsByTgt: make(map[URN]map[URN]struct{}),
	}
}

// Rebuild reconstructs the secondary indexes from Nodes and Relations.
// Call after loading a state from JSON (which lacks the json:"-" fields)
// or after any code path that mutated Nodes/Relations without going
// through fold. Idempotent.
//
// Cost: O(len(Nodes) + len(Relations)). Typically called once at
// kernel start (fold.Replay) and never again.
func (s *GraphState) Rebuild() {
	s.NodesByType = make(map[TypeID]map[URN]struct{}, len(s.Nodes))
	for urn, n := range s.Nodes {
		IndexAddNodeByType(s.NodesByType, urn, n.TypeID)
	}
	s.RelationsBySrc = make(map[URN]map[URN]struct{}, len(s.Relations))
	s.RelationsByTgt = make(map[URN]map[URN]struct{}, len(s.Relations))
	for urn, r := range s.Relations {
		IndexAddRelationEndpoints(s.RelationsBySrc, s.RelationsByTgt, urn, r.SrcURN, r.TgtURN)
	}
}

// Clone returns a deep copy of the state. Used by fold before applying rewrites
// so the original state is untouched on failure.
//
// Clones Nodes and Relations deeply; the secondary indexes are deep-copied
// too (shallow reuse would let fold mutations on the clone leak into the
// caller's map, breaking the "state-on-failure is unchanged" guarantee).
func (s GraphState) Clone() GraphState {
	c := GraphState{
		Nodes:          make(map[URN]Node, len(s.Nodes)),
		Relations:      make(map[URN]Relation, len(s.Relations)),
		NodesByType:    cloneURNSetMap(s.NodesByType),
		RelationsBySrc: cloneURNURNSet(s.RelationsBySrc),
		RelationsByTgt: cloneURNURNSet(s.RelationsByTgt),
	}
	for k, v := range s.Nodes {
		// Deep copy properties map
		props := make(map[string]Property, len(v.Properties))
		for pk, pv := range v.Properties {
			props[pk] = pv
		}
		v.Properties = props
		c.Nodes[k] = v
	}
	for k, v := range s.Relations {
		c.Relations[k] = v
	}
	return c
}

// ----------------------------------------------------------------------------
// Accessor methods — consumers should prefer these over reading the index
// maps directly. They tolerate a nil/uninitialized index by falling back
// to a full scan of the backing map, so hand-crafted test states work
// without an explicit Rebuild call (though Rebuild is still recommended
// for clarity and for test states with many nodes).
// ----------------------------------------------------------------------------

// NodesOfType returns all node URNs with the given TypeID.
//
// Uses the NodesByType index when initialized (O(bucket-size), the fast
// path that production code takes after NewRuntime → fold.Replay). Falls
// back to a full O(all-nodes) scan when NodesByType is nil — this keeps
// hand-crafted test fixtures correct without requiring an explicit
// Rebuild call. Semantic boundary:
//
//   - NodesByType == nil      → index not initialized → scan
//   - NodesByType != nil      → trust the index (possibly empty bucket)
//
// Return order is not defined.
func (s *GraphState) NodesOfType(typeID TypeID) []URN {
	if s.NodesByType != nil {
		set := s.NodesByType[typeID]
		out := make([]URN, 0, len(set))
		for urn := range set {
			out = append(out, urn)
		}
		return out
	}
	var out []URN
	for urn, n := range s.Nodes {
		if n.TypeID == typeID {
			out = append(out, urn)
		}
	}
	return out
}

// RelationsFrom returns all relation URNs with the given SrcURN.
//
// Uses RelationsBySrc when initialized; falls back to a full scan
// otherwise (see NodesOfType for the semantic boundary).
func (s *GraphState) RelationsFrom(src URN) []URN {
	if s.RelationsBySrc != nil {
		set := s.RelationsBySrc[src]
		out := make([]URN, 0, len(set))
		for urn := range set {
			out = append(out, urn)
		}
		return out
	}
	var out []URN
	for urn, r := range s.Relations {
		if r.SrcURN == src {
			out = append(out, urn)
		}
	}
	return out
}

// RelationsTo returns all relation URNs with the given TgtURN.
//
// Uses RelationsByTgt when initialized; falls back to a full scan
// otherwise (see NodesOfType for the semantic boundary).
func (s *GraphState) RelationsTo(tgt URN) []URN {
	if s.RelationsByTgt != nil {
		set := s.RelationsByTgt[tgt]
		out := make([]URN, 0, len(set))
		for urn := range set {
			out = append(out, urn)
		}
		return out
	}
	var out []URN
	for urn, r := range s.Relations {
		if r.TgtURN == tgt {
			out = append(out, urn)
		}
	}
	return out
}

// ----------------------------------------------------------------------------
// Index helpers — exposed so fold.Evaluate can maintain them on apply, and
// so tests can construct pre-populated states without going through Rebuild.
// ----------------------------------------------------------------------------

// IndexAddNodeByType records a node in the by-type index. Tolerates a nil
// outer map (no-op) so callers that haven't initialized indexes don't panic.
func IndexAddNodeByType(idx map[TypeID]map[URN]struct{}, nodeURN URN, typeID TypeID) {
	if idx == nil {
		return
	}
	set, ok := idx[typeID]
	if !ok {
		set = make(map[URN]struct{})
		idx[typeID] = set
	}
	set[nodeURN] = struct{}{}
}

// IndexRemoveNodeByType removes a node from the by-type index. Tolerates
// nil outer map and missing entries.
func IndexRemoveNodeByType(idx map[TypeID]map[URN]struct{}, nodeURN URN, typeID TypeID) {
	if idx == nil {
		return
	}
	set, ok := idx[typeID]
	if !ok {
		return
	}
	delete(set, nodeURN)
	if len(set) == 0 {
		delete(idx, typeID)
	}
}

// IndexAddRelationEndpoints records a relation in both endpoint indexes.
// Tolerates nil maps.
func IndexAddRelationEndpoints(bySrc, byTgt map[URN]map[URN]struct{}, relURN, srcURN, tgtURN URN) {
	if bySrc != nil && srcURN != "" {
		set, ok := bySrc[srcURN]
		if !ok {
			set = make(map[URN]struct{})
			bySrc[srcURN] = set
		}
		set[relURN] = struct{}{}
	}
	if byTgt != nil && tgtURN != "" {
		set, ok := byTgt[tgtURN]
		if !ok {
			set = make(map[URN]struct{})
			byTgt[tgtURN] = set
		}
		set[relURN] = struct{}{}
	}
}

// IndexRemoveRelationEndpoints removes a relation from both endpoint
// indexes. Tolerates nil maps and missing entries.
func IndexRemoveRelationEndpoints(bySrc, byTgt map[URN]map[URN]struct{}, relURN, srcURN, tgtURN URN) {
	if bySrc != nil && srcURN != "" {
		if set, ok := bySrc[srcURN]; ok {
			delete(set, relURN)
			if len(set) == 0 {
				delete(bySrc, srcURN)
			}
		}
	}
	if byTgt != nil && tgtURN != "" {
		if set, ok := byTgt[tgtURN]; ok {
			delete(set, relURN)
			if len(set) == 0 {
				delete(byTgt, tgtURN)
			}
		}
	}
}

// cloneURNSetMap deep-copies a map[TypeID]map[URN]struct{}.
func cloneURNSetMap(src map[TypeID]map[URN]struct{}) map[TypeID]map[URN]struct{} {
	if src == nil {
		return make(map[TypeID]map[URN]struct{})
	}
	out := make(map[TypeID]map[URN]struct{}, len(src))
	for k, set := range src {
		cp := make(map[URN]struct{}, len(set))
		for u := range set {
			cp[u] = struct{}{}
		}
		out[k] = cp
	}
	return out
}

// cloneURNURNSet deep-copies a map[URN]map[URN]struct{}.
func cloneURNURNSet(src map[URN]map[URN]struct{}) map[URN]map[URN]struct{} {
	if src == nil {
		return make(map[URN]map[URN]struct{})
	}
	out := make(map[URN]map[URN]struct{}, len(src))
	for k, set := range src {
		cp := make(map[URN]struct{}, len(set))
		for u := range set {
			cp[u] = struct{}{}
		}
		out[k] = cp
	}
	return out
}
