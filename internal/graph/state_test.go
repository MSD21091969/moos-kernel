package graph

import "testing"

// TestNewGraphState_IndexesInitialized — the constructor returns a state
// whose secondary indexes are all non-nil (empty) so hot-path consumers
// don't have to nil-check.
func TestNewGraphState_IndexesInitialized(t *testing.T) {
	s := NewGraphState()
	if s.NodesByType == nil {
		t.Error("NewGraphState.NodesByType must be non-nil")
	}
	if s.RelationsBySrc == nil {
		t.Error("NewGraphState.RelationsBySrc must be non-nil")
	}
	if s.RelationsByTgt == nil {
		t.Error("NewGraphState.RelationsByTgt must be non-nil")
	}
}

// TestNodesOfType_Fallback — a hand-constructed state with nil index
// falls back to a full scan and still returns the correct result.
func TestNodesOfType_Fallback(t *testing.T) {
	s := GraphState{
		Nodes: map[URN]Node{
			"urn:a": {URN: "urn:a", TypeID: "program"},
			"urn:b": {URN: "urn:b", TypeID: "t_hook"},
			"urn:c": {URN: "urn:c", TypeID: "program"},
		},
		Relations: map[URN]Relation{},
		// NodesByType intentionally nil
	}
	got := s.NodesOfType("program")
	if len(got) != 2 {
		t.Errorf("expected 2 program nodes via scan fallback, got %d: %v", len(got), got)
	}
}

// TestNodesOfType_IndexedFast — when the index is populated the accessor
// reads from it directly. We verify by mutating the underlying map only
// (no index update) and asserting the accessor still returns the index
// view.
func TestNodesOfType_IndexedFast(t *testing.T) {
	s := NewGraphState()
	s.Nodes["urn:a"] = Node{URN: "urn:a", TypeID: "program"}
	IndexAddNodeByType(s.NodesByType, "urn:a", "program")

	// Now add a node DIRECTLY to the map without updating the index —
	// simulates a fold bug. The index-backed accessor should NOT see it;
	// that's the indexed-is-truth contract.
	s.Nodes["urn:b"] = Node{URN: "urn:b", TypeID: "program"}
	got := s.NodesOfType("program")
	if len(got) != 1 || got[0] != "urn:a" {
		t.Errorf("indexed accessor must not see urn:b (index not updated); got %v", got)
	}
}

// TestRelationsFrom_IndexedFast — same invariant for the relation
// outbound index.
func TestRelationsFrom_IndexedFast(t *testing.T) {
	s := NewGraphState()
	s.Relations["r1"] = Relation{URN: "r1", SrcURN: "urn:a", TgtURN: "urn:b"}
	IndexAddRelationEndpoints(s.RelationsBySrc, s.RelationsByTgt, "r1", "urn:a", "urn:b")

	got := s.RelationsFrom("urn:a")
	if len(got) != 1 || got[0] != "r1" {
		t.Errorf("RelationsFrom(urn:a) = %v; want [r1]", got)
	}

	gotTo := s.RelationsTo("urn:b")
	if len(gotTo) != 1 || gotTo[0] != "r1" {
		t.Errorf("RelationsTo(urn:b) = %v; want [r1]", gotTo)
	}
}

// TestRebuild_ReconstructsFromScratch — a state that lost its indexes
// (e.g. after JSON round-trip) gets them back via Rebuild.
func TestRebuild_ReconstructsFromScratch(t *testing.T) {
	s := GraphState{
		Nodes: map[URN]Node{
			"urn:x": {URN: "urn:x", TypeID: "program"},
			"urn:y": {URN: "urn:y", TypeID: "t_hook"},
		},
		Relations: map[URN]Relation{
			"r1": {URN: "r1", SrcURN: "urn:x", TgtURN: "urn:y"},
		},
	}
	s.Rebuild()

	if len(s.NodesByType["program"]) != 1 {
		t.Errorf("Rebuild didn't index program nodes: %v", s.NodesByType)
	}
	if len(s.NodesByType["t_hook"]) != 1 {
		t.Errorf("Rebuild didn't index t_hook nodes: %v", s.NodesByType)
	}
	if len(s.RelationsBySrc["urn:x"]) != 1 {
		t.Errorf("Rebuild didn't index outbound relation: %v", s.RelationsBySrc)
	}
	if len(s.RelationsByTgt["urn:y"]) != 1 {
		t.Errorf("Rebuild didn't index inbound relation: %v", s.RelationsByTgt)
	}
}

// TestClone_DeepCopiesIndexes — mutating the clone's indexes must not
// affect the original. Guards the "state-on-failure unchanged" guarantee
// that fold relies on.
func TestClone_DeepCopiesIndexes(t *testing.T) {
	s := NewGraphState()
	s.Nodes["urn:a"] = Node{URN: "urn:a", TypeID: "program"}
	IndexAddNodeByType(s.NodesByType, "urn:a", "program")
	s.Relations["r1"] = Relation{URN: "r1", SrcURN: "urn:a", TgtURN: "urn:a"}
	IndexAddRelationEndpoints(s.RelationsBySrc, s.RelationsByTgt, "r1", "urn:a", "urn:a")

	clone := s.Clone()
	// Mutate the clone's index.
	IndexAddNodeByType(clone.NodesByType, "urn:b", "t_hook")
	IndexAddRelationEndpoints(clone.RelationsBySrc, clone.RelationsByTgt, "r2", "urn:b", "urn:a")

	// Original must not have seen any of it.
	if _, ok := s.NodesByType["t_hook"]; ok {
		t.Errorf("original NodesByType mutated by clone")
	}
	if _, ok := s.RelationsBySrc["urn:b"]; ok {
		t.Errorf("original RelationsBySrc mutated by clone")
	}
	// urn:a still in the original with its original single entry.
	if len(s.RelationsByTgt["urn:a"]) != 1 {
		t.Errorf("original RelationsByTgt[urn:a] should have exactly 1 entry, got %d", len(s.RelationsByTgt["urn:a"]))
	}
}

// TestIndexRemoveRelationEndpoints_CleansEmptySets — after removing the
// last relation for a source, the outer map no longer carries an empty
// inner map (so len() reflects true sources, not zombies).
func TestIndexRemoveRelationEndpoints_CleansEmptySets(t *testing.T) {
	bySrc := map[URN]map[URN]struct{}{}
	byTgt := map[URN]map[URN]struct{}{}
	IndexAddRelationEndpoints(bySrc, byTgt, "r1", "urn:a", "urn:b")
	IndexRemoveRelationEndpoints(bySrc, byTgt, "r1", "urn:a", "urn:b")
	if _, ok := bySrc["urn:a"]; ok {
		t.Errorf("RelationsBySrc should not retain empty bucket for urn:a")
	}
	if _, ok := byTgt["urn:b"]; ok {
		t.Errorf("RelationsByTgt should not retain empty bucket for urn:b")
	}
}
