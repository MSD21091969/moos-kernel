package graph

import "time"

// RewriteType names the four and only four graph operations.
// Nothing else exists. An edge does not "do" anything.
// A node does not "call" anything. The kernel receives a rewrite request,
// validates it against constraints, applies it, and logs it.
type RewriteType string

const (
	ADD    RewriteType = "ADD"
	LINK   RewriteType = "LINK"
	MUTATE RewriteType = "MUTATE"
	UNLINK RewriteType = "UNLINK"
)

// Envelope is a rewrite request. One envelope = one atomic graph operation.
// For atomic multi-step operations, use a Program ([]Envelope evaluated together).
//
// Field usage by rewrite type:
//
//	ADD:    NodeURN, TypeID, Properties, Actor
//	LINK:   RelationURN, SrcURN, SrcPort, TgtURN, TgtPort, RewriteCategory, ContractURN (WF15), Actor
//	MUTATE: TargetURN, Field, NewValue, ExpectedVersion (0 = skip CAS), RewriteCategory, Actor
//	UNLINK: RelationURN, Actor
type Envelope struct {
	RewriteType RewriteType `json:"rewrite_type"`
	Actor       URN         `json:"actor"` // URN of the node requesting this rewrite

	// ADD fields
	NodeURN    URN                 `json:"node_urn,omitempty"`
	TypeID     TypeID              `json:"type_id,omitempty"`
	Properties map[string]Property `json:"properties,omitempty"`

	// LINK fields
	RelationURN     URN             `json:"relation_urn,omitempty"`
	SrcURN          URN             `json:"src_urn,omitempty"`
	SrcPort         string          `json:"src_port,omitempty"`
	TgtURN          URN             `json:"tgt_urn,omitempty"`
	TgtPort         string          `json:"tgt_port,omitempty"`
	RewriteCategory RewriteCategory `json:"rewrite_category,omitempty"`
	ContractURN     URN             `json:"contract_urn,omitempty"` // required for WF15

	// MUTATE fields
	TargetURN       URN      `json:"target_urn,omitempty"`
	Field           string   `json:"field,omitempty"`
	NewValue        any      `json:"new_value,omitempty"`
	ExpectedVersion int64    `json:"expected_version,omitempty"` // 0 = skip CAS
	PropertySpec    *Property `json:"property_spec,omitempty"`   // injected for additive MUTATE (field not yet on node)
}

// EvalResult is the outcome of applying a single Envelope.
type EvalResult struct {
	AffectedNodeURN     URN `json:"affected_node_urn,omitempty"`
	AffectedRelationURN URN `json:"affected_relation_urn,omitempty"`
}

// PersistedRewrite is the log entry written after successful application.
// The append-only log is truth. state(t) = fold(log[0..t]).
type PersistedRewrite struct {
	Envelope  Envelope  `json:"envelope"`
	AppliedAt time.Time `json:"applied_at"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	LogSeq    int64     `json:"log_seq"`
}
