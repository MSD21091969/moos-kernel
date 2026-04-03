package fold

import (
	"errors"
	"fmt"
	slog "log"

	"moos/kernel/internal/graph"
)

// Replay folds a persisted rewrite log from an empty state,
// reproducing the current graph state deterministically.
// CI-4: same log → same state, always.
//
// Idempotent error handling — the following are skipped with a warning,
// never treated as corruption:
//   - ErrNodeExists       on ADD  (already applied)
//   - ErrRelationExists   on LINK (already applied)
//   - ErrRelationNotFound on UNLINK (already removed)
//   - ErrVersionConflict  on MUTATE (already at later version)
//
// Any other error halts replay and returns the partial state with the error.
func Replay(entries []graph.PersistedRewrite) (graph.GraphState, error) {
	state := graph.NewGraphState()

	for i, pr := range entries {
		next, _, err := Evaluate(state, pr.Envelope)
		if err != nil {
			if isIdempotentSkip(pr.Envelope.RewriteType, err) {
				slog.Printf("replay: skipping idempotent error at seq %d (index %d, %s): %v",
					pr.LogSeq, i, pr.Envelope.RewriteType, err)
				continue
			}
			return state, &ReplayError{Seq: pr.LogSeq, Index: i, Cause: err}
		}
		state = next
	}
	return state, nil
}

func isIdempotentSkip(rt graph.RewriteType, err error) bool {
	switch rt {
	case graph.ADD:
		return errors.Is(err, ErrNodeExists)
	case graph.LINK:
		return errors.Is(err, ErrRelationExists)
	case graph.UNLINK:
		return errors.Is(err, ErrRelationNotFound)
	case graph.MUTATE:
		return errors.Is(err, ErrVersionConflict)
	}
	return false
}

// ReplayError wraps a replay failure with position context.
type ReplayError struct {
	Seq   int64
	Index int
	Cause error
}

func (e *ReplayError) Error() string {
	return fmt.Sprintf("replay failed at seq %d (index %d): %v", e.Seq, e.Index, e.Cause)
}

func (e *ReplayError) Unwrap() error { return e.Cause }
