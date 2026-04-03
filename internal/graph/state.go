package graph

// GraphState is the current materialized state of the kernel graph.
// It is DERIVED — always reproducible from fold(log[0..t]).
// state(t) = fold(log[0..t])  (CI-4: same log → same state, always)
//
// Never treat GraphState as the source of truth. The append-only rewrite log is truth.
type GraphState struct {
	Nodes     map[URN]Node     `json:"nodes"`
	Relations map[URN]Relation `json:"relations"`
}

func NewGraphState() GraphState {
	return GraphState{
		Nodes:     make(map[URN]Node),
		Relations: make(map[URN]Relation),
	}
}

// Clone returns a deep copy of the state. Used by fold before applying rewrites
// so the original state is untouched on failure.
func (s GraphState) Clone() GraphState {
	c := GraphState{
		Nodes:     make(map[URN]Node, len(s.Nodes)),
		Relations: make(map[URN]Relation, len(s.Relations)),
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
