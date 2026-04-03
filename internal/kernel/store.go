package kernel

import "moos/kernel/internal/graph"

// Store is the append-only persistence interface for the rewrite log.
// The log is truth — state is derived from it via fold.Replay.
// Implementations: LogStore (JSONL file), MemStore (in-memory, for tests).
type Store interface {
	Append(entries []graph.PersistedRewrite) error
	ReadAll() ([]graph.PersistedRewrite, error)
}
