package graph

// Property is a typed, constrained key-value pair bound to a node.
// Properties are NOT opaque payload blobs. They are NOT edges to value-nodes.
// They are NOT denormalized topology caches.
//
// Change mechanism: MUTATE rewrite with preconditions:
//   - Field must be in allowed_fields for the node type AND the rewrite category
//   - Field must NOT be immutable
//   - Requester must satisfy AuthorityScope
//
// Global immutables (no MUTATE ever, on any node type):
//   - Identity URN
//   - CreatedAt
//   - OwnerURN (any _urn suffix immutable property set at ADD time)
//   - SenderURN (on interaction nodes)
type Property struct {
	Value          any     `json:"value"`
	Mutability     string  `json:"mutability"`      // "immutable" | "mutable"
	AuthorityScope string  `json:"authority_scope"` // "kernel"|"owner"|"principal"|"substrate"|"delegate"; empty if immutable
	StratumOrigin  Stratum `json:"stratum_origin"`
	ValidationType string  `json:"validation_type,omitempty"` // type hint for value validation
}

// Immutable returns true if this property can never be changed via MUTATE.
func (p Property) Immutable() bool { return p.Mutability == "immutable" }
