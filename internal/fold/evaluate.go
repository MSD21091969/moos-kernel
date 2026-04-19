package fold

import (
	"fmt"
	"time"

	"moos/kernel/internal/graph"
)

// Evaluate applies one Envelope to the given state and returns the new state.
// It is pure — no IO, no locks, no side effects.
// On failure, the original state is returned unchanged.
//
// Validation against the operad (type-checking, port color compatibility,
// authority scope) is NOT done here — that is the operad layer's responsibility.
// fold only enforces structural invariants: existence, immutability, version.
func Evaluate(state graph.GraphState, env graph.Envelope) (graph.GraphState, graph.EvalResult, error) {
	if err := validateEnvelopeStructure(env); err != nil {
		return state, graph.EvalResult{}, err
	}

	switch env.RewriteType {
	case graph.ADD:
		return applyADD(state, env)
	case graph.LINK:
		return applyLINK(state, env)
	case graph.MUTATE:
		return applyMUTATE(state, env)
	case graph.UNLINK:
		return applyUNLINK(state, env)
	default:
		return state, graph.EvalResult{}, fmt.Errorf("%w: %q", ErrInvalidRewriteType, env.RewriteType)
	}
}

func applyADD(state graph.GraphState, env graph.Envelope) (graph.GraphState, graph.EvalResult, error) {
	if _, exists := state.Nodes[env.NodeURN]; exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: %s", ErrNodeExists, env.NodeURN)
	}

	props := make(map[string]graph.Property, len(env.Properties))
	for k, v := range env.Properties {
		props[k] = v
	}

	next := state.Clone()
	next.Nodes[env.NodeURN] = graph.Node{
		URN:        env.NodeURN,
		TypeID:     env.TypeID,
		Properties: props,
		CreatedAt:  time.Now().UTC(),
		Version:    1,
	}
	// Maintain the NodesByType index so sweep/t-cone can look up nodes by
	// TypeID in O(bucket-size) instead of O(all-nodes). The helper tolerates
	// a nil map (older states created without NewGraphState).
	graph.IndexAddNodeByType(next.NodesByType, env.NodeURN, env.TypeID)
	return next, graph.EvalResult{AffectedNodeURN: env.NodeURN}, nil
}

func applyLINK(state graph.GraphState, env graph.Envelope) (graph.GraphState, graph.EvalResult, error) {
	if _, exists := state.Nodes[env.SrcURN]; !exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: src %s", ErrNodeNotFound, env.SrcURN)
	}
	if _, exists := state.Nodes[env.TgtURN]; !exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: tgt %s", ErrNodeNotFound, env.TgtURN)
	}
	if _, exists := state.Relations[env.RelationURN]; exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: %s", ErrRelationExists, env.RelationURN)
	}
	if env.RewriteCategory == graph.WF15 && env.ContractURN == "" {
		return state, graph.EvalResult{}, ErrWF15MissingContract
	}

	next := state.Clone()
	next.Relations[env.RelationURN] = graph.Relation{
		URN:             env.RelationURN,
		RewriteCategory: env.RewriteCategory,
		SrcURN:          env.SrcURN,
		SrcPort:         env.SrcPort,
		TgtURN:          env.TgtURN,
		TgtPort:         env.TgtPort,
		ContractURN:     env.ContractURN,
		CreatedAt:       time.Now().UTC(),
	}
	// Maintain the by-endpoint relation indexes so occupancy walks and
	// t-cone neighbourhood queries run in O(edges-at-urn) instead of
	// O(all-relations).
	graph.IndexAddRelationEndpoints(next.RelationsBySrc, next.RelationsByTgt, env.RelationURN, env.SrcURN, env.TgtURN)
	return next, graph.EvalResult{AffectedRelationURN: env.RelationURN}, nil
}

func applyMUTATE(state graph.GraphState, env graph.Envelope) (graph.GraphState, graph.EvalResult, error) {
	node, exists := state.Nodes[env.TargetURN]
	if !exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: %s", ErrNodeNotFound, env.TargetURN)
	}

	prop, hasProp := node.Properties[env.Field]
	if !hasProp {
		// Additive MUTATE: field not yet on this node — use injected PropertySpec from runtime
		// (runtime validates against ontology and injects spec for new optional properties).
		if env.PropertySpec == nil {
			return state, graph.EvalResult{}, fmt.Errorf("%w: field %q not found on node %s", ErrFieldNotInScope, env.Field, env.TargetURN)
		}
		next := state.Clone()
		mutated := next.Nodes[env.TargetURN]
		mutated.Version++
		mutated.Properties[env.Field] = graph.Property{
			Value:          env.NewValue,
			Mutability:     env.PropertySpec.Mutability,
			AuthorityScope: env.PropertySpec.AuthorityScope,
			StratumOrigin:  env.PropertySpec.StratumOrigin,
			ValidationType: env.PropertySpec.ValidationType,
		}
		next.Nodes[env.TargetURN] = mutated
		return next, graph.EvalResult{AffectedNodeURN: env.TargetURN}, nil
	}
	if prop.Immutable() {
		return state, graph.EvalResult{}, fmt.Errorf("%w: field %q on node %s", ErrImmutableProperty, env.Field, env.TargetURN)
	}

	// Optimistic CAS: reject if caller expects a specific version but node has diverged
	if env.ExpectedVersion != 0 && node.Version != env.ExpectedVersion {
		return state, graph.EvalResult{}, fmt.Errorf("%w: expected %d got %d", ErrVersionConflict, env.ExpectedVersion, node.Version)
	}

	next := state.Clone()
	mutated := next.Nodes[env.TargetURN]
	mutated.Version++
	mutated.Properties[env.Field] = graph.Property{
		Value:          env.NewValue,
		Mutability:     prop.Mutability,
		AuthorityScope: prop.AuthorityScope,
		StratumOrigin:  prop.StratumOrigin,
		ValidationType: prop.ValidationType,
	}
	next.Nodes[env.TargetURN] = mutated
	return next, graph.EvalResult{AffectedNodeURN: env.TargetURN}, nil
}

func applyUNLINK(state graph.GraphState, env graph.Envelope) (graph.GraphState, graph.EvalResult, error) {
	existing, exists := state.Relations[env.RelationURN]
	if !exists {
		return state, graph.EvalResult{}, fmt.Errorf("%w: %s", ErrRelationNotFound, env.RelationURN)
	}
	next := state.Clone()
	delete(next.Relations, env.RelationURN)
	// Remove the relation from both endpoint indexes so subsequent walks
	// don't see a stale URN.
	graph.IndexRemoveRelationEndpoints(next.RelationsBySrc, next.RelationsByTgt, env.RelationURN, existing.SrcURN, existing.TgtURN)
	return next, graph.EvalResult{AffectedRelationURN: env.RelationURN}, nil
}

func validateEnvelopeStructure(env graph.Envelope) error {
	if env.Actor == "" {
		return ErrMissingActor
	}
	switch env.RewriteType {
	case graph.ADD:
		if env.NodeURN == "" {
			return ErrMissingNodeURN
		}
		if env.TypeID == "" {
			return ErrMissingTypeID
		}
	case graph.LINK:
		if env.RelationURN == "" {
			return ErrMissingRelationURN
		}
		if env.SrcURN == "" || env.TgtURN == "" {
			return ErrMissingSrcTgt
		}
	case graph.MUTATE:
		if env.TargetURN == "" || env.Field == "" {
			return ErrMissingTargetField
		}
	case graph.UNLINK:
		if env.RelationURN == "" {
			return ErrMissingRelationURN
		}
	}
	return nil
}
