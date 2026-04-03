package fold

import "errors"

// Sentinel errors for the fold layer.
// These are returned by Evaluate and matched by Replay for idempotent skip logic.

var (
	ErrNodeExists      = errors.New("node already exists")
	ErrNodeNotFound    = errors.New("node not found")
	ErrRelationExists  = errors.New("relation already exists")
	ErrRelationNotFound = errors.New("relation not found")

	ErrImmutableProperty = errors.New("property is immutable — cannot MUTATE")
	ErrFieldNotInScope   = errors.New("field not in MUTATE scope for this rewrite category")
	ErrUnauthorized      = errors.New("actor does not satisfy authority_scope for this field")
	ErrVersionConflict   = errors.New("optimistic CAS failed — version mismatch")

	ErrInvalidRewriteType = errors.New("unknown rewrite type")
	ErrMissingActor       = errors.New("envelope missing actor URN")
	ErrMissingNodeURN     = errors.New("ADD envelope missing node_urn")
	ErrMissingTypeID      = errors.New("ADD envelope missing type_id")
	ErrMissingRelationURN = errors.New("LINK/UNLINK envelope missing relation_urn")
	ErrMissingSrcTgt      = errors.New("LINK envelope missing src_urn or tgt_urn")
	ErrMissingTargetField = errors.New("MUTATE envelope missing target_urn or field")
	ErrWF15MissingContract = errors.New("WF15 relation requires contract_urn")
)
