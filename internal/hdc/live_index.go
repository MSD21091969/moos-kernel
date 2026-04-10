package hdc

import (
	"sync"

	"moos/kernel/internal/graph"
)

// LiveIndex keeps the latest type-expression view in memory.
type LiveIndex struct {
	mu          sync.RWMutex
	threshold   float64
	expressions []TypeExpressionEntry
}

func NewLiveIndex(threshold float64) *LiveIndex {
	if threshold <= 0 {
		threshold = 0.3
	}
	return &LiveIndex{threshold: threshold}
}

func (li *LiveIndex) Recompute(state graph.GraphState, enc *Encoder) {
	rows := TypeExpressions(state, enc)
	li.mu.Lock()
	defer li.mu.Unlock()
	li.expressions = deepCopyExpressions(rows)
}

func (li *LiveIndex) Expressions() []TypeExpressionEntry {
	li.mu.RLock()
	defer li.mu.RUnlock()
	return deepCopyExpressions(li.expressions)
}

func (li *LiveIndex) Drifted() []TypeExpressionEntry {
	li.mu.RLock()
	defer li.mu.RUnlock()
	out := make([]TypeExpressionEntry, 0, len(li.expressions))
	for _, row := range li.expressions {
		if row.Drift > li.threshold {
			out = append(out, row)
		}
	}
	return deepCopyExpressions(out)
}

func deepCopyExpressions(in []TypeExpressionEntry) []TypeExpressionEntry {
	out := make([]TypeExpressionEntry, len(in))
	for i := range in {
		out[i] = in[i]
		if len(in[i].Top3Types) > 0 {
			out[i].Top3Types = make([]TypeScore, len(in[i].Top3Types))
			copy(out[i].Top3Types, in[i].Top3Types)
		}
	}
	return out
}
