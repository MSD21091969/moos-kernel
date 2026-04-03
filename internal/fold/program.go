package fold

import (
	"fmt"
	"time"

	"moos/kernel/internal/graph"
)

// EvaluateProgram applies a slice of Envelopes atomically.
// If any Envelope fails, the entire program is rolled back —
// the original state is returned unchanged.
// On success, returns the new state and per-envelope results.
func EvaluateProgram(state graph.GraphState, envelopes []graph.Envelope) (graph.GraphState, []graph.EvalResult, error) {
	working := state.Clone()
	results := make([]graph.EvalResult, 0, len(envelopes))

	for i, env := range envelopes {
		next, result, err := Evaluate(working, env)
		if err != nil {
			return state, nil, fmt.Errorf("program step %d (%s): %w", i, env.RewriteType, err)
		}
		working = next
		results = append(results, result)
	}
	return working, results, nil
}

// ProgramResult is returned by kernel.Runtime.ApplyProgram.
type ProgramResult struct {
	Results   []graph.EvalResult   `json:"results"`
	Persisted []graph.PersistedRewrite `json:"persisted"`
	AppliedAt time.Time            `json:"applied_at"`
}
