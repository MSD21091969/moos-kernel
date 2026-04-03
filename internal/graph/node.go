package graph

import "time"

// TypeID is the node type identifier, matching the operad registry.
// Examples: "user", "agent", "kernel", "session", "capability", "endpoint"
type TypeID string

// Node is an identity point in the graph.
// Identity is its URN — stable, not content-addressed.
// Changing properties does not change identity (CI-3).
// An ADD rewrite creates a node. No rewrite deletes it.
type Node struct {
	URN        URN                `json:"urn"`
	TypeID     TypeID             `json:"type_id"`
	Properties map[string]Property `json:"properties"`
	CreatedAt  time.Time          `json:"created_at"`
	// Version is a monotonic counter incremented on every MUTATE.
	// Used for optimistic CAS in MUTATE rewrites.
	Version int64 `json:"version"`
}
