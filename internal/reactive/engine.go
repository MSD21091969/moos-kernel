// Package reactive implements the Watch/React/Guard evaluation engine.
// Watch = presheaf on rewrite log (pattern match against rewrites)
// React = natural transformation (matched rewrite → proposed new rewrite)
// Guard = subobject classifier (state predicate that gates a reactor)
package reactive

import (
	"encoding/json"
	"fmt"
	"strings"

	"moos/kernel/internal/graph"
)

// Engine evaluates watcher/reactor/guard nodes against incoming rewrites,
// as well as t_hook nodes owned by the affected node (M6 first-class t-hooks).
// It is read-only over graph state — it never mutates the graph directly.
type Engine struct {
	State *graph.GraphState
}

// Evaluate takes a just-applied rewrite and returns proposed rewrites from:
//  1. Watcher→Reactor chains (existing WF17 pattern) whose guards all pass.
//  2. T-hook nodes (M6) owned by the affected node whose event_shape + guard_ref pass.
func (e *Engine) Evaluate(rewrite graph.PersistedRewrite) []graph.Envelope {
	var proposals []graph.Envelope

	env := rewrite.Envelope

	// Determine the affected node URN and type_id from the envelope.
	affectedURN, affectedTypeID := e.affected(env)

	// Pass 1: Watcher/Guard/Reactor chain (WF17 pattern — unchanged).
	for _, node := range e.State.Nodes {
		if node.TypeID != "watcher" {
			continue
		}
		if !e.watcherMatches(node, env, affectedURN, affectedTypeID) {
			continue
		}
		if !e.guardsPass(node.URN) {
			continue
		}
		proposed := e.react(node.URN, affectedURN, affectedTypeID, env.Actor)
		proposals = append(proposals, proposed...)
	}

	// Pass 2: T-hooks owned by the affected node (M6 first-class hooks).
	// A t_hook fires when its owner_urn == affectedURN, event_shape matches,
	// and the optional guard_ref predicate passes. No WF LINK required.
	if affectedURN != "" {
		for _, thook := range e.State.Nodes {
			if thook.TypeID != "t_hook" {
				continue
			}
			if propStr(thook, "status") != "active" {
				continue
			}
			if propStr(thook, "owner_urn") != string(affectedURN) {
				continue
			}
			if !e.thookEventShapeMatches(thook, env, affectedTypeID) {
				continue
			}
			if !e.thookGuardPasses(thook) {
				continue
			}
			proposal, err := e.buildFromProp(thook, "react_template", affectedURN, affectedTypeID, env.Actor)
			if err != nil {
				continue
			}
			proposals = append(proposals, proposal)
		}
	}

	return proposals
}

// affected extracts the URN and type_id of the node affected by a rewrite.
func (e *Engine) affected(env graph.Envelope) (graph.URN, graph.TypeID) {
	switch env.RewriteType {
	case graph.ADD:
		return env.NodeURN, env.TypeID
	case graph.LINK:
		// For LINK, the "affected" node is the source.
		if n, ok := e.State.Nodes[env.SrcURN]; ok {
			return env.SrcURN, n.TypeID
		}
		return env.SrcURN, ""
	case graph.MUTATE:
		if n, ok := e.State.Nodes[env.TargetURN]; ok {
			return env.TargetURN, n.TypeID
		}
		return env.TargetURN, ""
	case graph.UNLINK:
		// For UNLINK, look up the relation to find the source node.
		if rel, ok := e.State.Relations[env.RelationURN]; ok {
			if n, ok := e.State.Nodes[rel.SrcURN]; ok {
				return rel.SrcURN, n.TypeID
			}
		}
		return "", ""
	}
	return "", ""
}

// watcherMatches checks whether a watcher node's pattern matches the rewrite.
func (e *Engine) watcherMatches(watcher graph.Node, env graph.Envelope, affectedURN graph.URN, affectedTypeID graph.TypeID) bool {
	// Must be active.
	if propStr(watcher, "status") != "active" {
		return false
	}

	// match_rewrite_type
	if mrt := propStr(watcher, "match_rewrite_type"); mrt != "" && mrt != "*" {
		if mrt != string(env.RewriteType) {
			return false
		}
	}

	// match_type_id
	if mtid := propStr(watcher, "match_type_id"); mtid != "" && mtid != "*" {
		if mtid != string(affectedTypeID) {
			return false
		}
	}

	// match_urn_prefix
	if prefix := propStr(watcher, "match_urn_prefix"); prefix != "" {
		if !strings.HasPrefix(string(affectedURN), prefix) {
			return false
		}
	}

	// match_port — checks against src_port or tgt_port on LINK/UNLINK.
	if mp := propStr(watcher, "match_port"); mp != "" {
		if env.SrcPort != mp && env.TgtPort != mp {
			return false
		}
	}

	return true
}

// guardsPass checks that ALL guard nodes linked to the watcher via
// WF17 "guarded-by" pass. Returns true if there are no guards.
func (e *Engine) guardsPass(watcherURN graph.URN) bool {
	for _, rel := range e.State.Relations {
		// Guard → Watcher via "guards" / "guarded-by" ports.
		if rel.TgtURN != watcherURN || rel.TgtPort != "guarded-by" {
			continue
		}
		guardNode, ok := e.State.Nodes[rel.SrcURN]
		if !ok {
			return false // guard referenced but missing — fail closed
		}
		if !e.evaluateGuard(guardNode) {
			return false
		}
	}
	return true
}

// evaluateGuard evaluates a single guard predicate against current state.
func (e *Engine) evaluateGuard(guard graph.Node) bool {
	predType := propStr(guard, "predicate_type")
	targetURN := graph.URN(propStr(guard, "target_urn"))
	field := propStr(guard, "field")
	expected := propStr(guard, "expected_value")
	negate := propBool(guard, "negate")

	var result bool

	switch predType {
	case "node_property":
		if n, ok := e.State.Nodes[targetURN]; ok {
			if p, ok := n.Properties[field]; ok {
				result = fmt.Sprintf("%v", p.Value) == expected
			}
		}
	case "field_set":
		// M8 completeness predicate: field must be present on the target node (any value).
		if n, ok := e.State.Nodes[targetURN]; ok {
			_, result = n.Properties[field]
		}
	case "relation_exists":
		for _, rel := range e.State.Relations {
			if rel.SrcURN == targetURN || rel.TgtURN == targetURN {
				result = true
				break
			}
		}
	case "node_exists":
		_, result = e.State.Nodes[targetURN]
	case "custom":
		result = true // future extension — pass by default
	}

	if negate {
		return !result
	}
	return result
}

// EvaluatePredicate is the public form of evaluateGuard.
// Used by the kernel's gate-check path (M8) to evaluate gate nodes on the apply pathway.
// gate nodes use identical predicate structure to guard nodes but live on the apply path,
// not the reactive path — they fail-close rewrites rather than gating reactor proposals.
func (e *Engine) EvaluatePredicate(node graph.Node) bool {
	return e.evaluateGuard(node)
}

// react finds all reactors linked from the watcher via "triggers" port
// and builds proposed envelopes from their templates.
func (e *Engine) react(watcherURN, matchedURN graph.URN, matchedTypeID graph.TypeID, actor graph.URN) []graph.Envelope {
	var proposals []graph.Envelope

	for _, rel := range e.State.Relations {
		// Watcher → Reactor via "triggers" / "triggered-by" ports.
		if rel.SrcURN != watcherURN || rel.SrcPort != "triggers" {
			continue
		}
		reactor, ok := e.State.Nodes[rel.TgtURN]
		if !ok {
			continue
		}
		if propStr(reactor, "status") != "active" {
			continue
		}
		actionType := propStr(reactor, "action_type")
		if actionType != "rewrite" {
			continue // tool_call, notify — future extensions
		}
		env, err := e.buildFromTemplate(reactor, matchedURN, matchedTypeID, actor)
		if err != nil {
			continue // skip malformed templates
		}
		proposals = append(proposals, env)
	}

	return proposals
}

// buildFromTemplate parses the reactor's "template" property as a JSON envelope.
// Delegates to buildFromProp.
func (e *Engine) buildFromTemplate(reactor graph.Node, matchedURN graph.URN, matchedTypeID graph.TypeID, actor graph.URN) (graph.Envelope, error) {
	return e.buildFromProp(reactor, "template", matchedURN, matchedTypeID, actor)
}

// buildFromProp parses a named property on a node as a JSON envelope template,
// substituting $matched_urn, $matched_type_id, and $actor placeholders.
// Used by both reactor nodes ("template") and t_hook nodes ("react_template").
func (e *Engine) buildFromProp(node graph.Node, propName string, matchedURN graph.URN, matchedTypeID graph.TypeID, actor graph.URN) (graph.Envelope, error) {
	tmplProp, ok := node.Properties[propName]
	if !ok {
		return graph.Envelope{}, fmt.Errorf("%s %s: no %q property", node.TypeID, node.URN, propName)
	}

	// The template is stored as a JSON object (map or raw JSON).
	raw, err := json.Marshal(tmplProp.Value)
	if err != nil {
		return graph.Envelope{}, fmt.Errorf("%s %s: marshal %q: %w", node.TypeID, node.URN, propName, err)
	}

	// Substitute placeholders in the raw JSON string.
	s := string(raw)
	s = strings.ReplaceAll(s, "$matched_urn", string(matchedURN))
	s = strings.ReplaceAll(s, "$matched_type_id", string(matchedTypeID))
	s = strings.ReplaceAll(s, "$actor", string(actor))

	var env graph.Envelope
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		return graph.Envelope{}, fmt.Errorf("%s %s: unmarshal %q: %w", node.TypeID, node.URN, propName, err)
	}

	return env, nil
}

// thookEventShape is the JSON structure stored in a t_hook's event_shape property.
// All fields are optional — omit or set to "" / "*" to match any value.
type thookEventShape struct {
	RewriteType string `json:"rewrite_type"` // "" or "*" = any
	TypeID      string `json:"type_id"`      // "" or "*" = any
	URNPrefix   string `json:"urn_prefix"`   // "" = any; matched against owner_urn
	Port        string `json:"port"`         // "" = any (for LINK/UNLINK src_port or tgt_port)
	Field       string `json:"field"`        // "" or "*" = any (for MUTATE)
}

// thookEventShapeMatches checks whether a t_hook's event_shape filter passes
// for the given rewrite. Missing event_shape means "match all" (open hook).
//
// TODO(perf): this function is called once per t_hook per rewrite, and the
// JSON marshal+unmarshal round-trip on event_shape is pure overhead when the
// stored value is already map[string]any (the common case after log replay).
// Fast-path: type-switch on prop.Value.(map[string]any) and read fields
// directly without re-encoding. Stored value cached on the node at ADD time
// would be even better, but requires extending the graph types (PR #8
// review, Gemini).
func (e *Engine) thookEventShapeMatches(thook graph.Node, env graph.Envelope, affectedTypeID graph.TypeID) bool {
	prop, ok := thook.Properties["event_shape"]
	if !ok {
		return true // no filter — fire on every rewrite that affects the owner
	}
	raw, err := json.Marshal(prop.Value)
	if err != nil {
		return false
	}
	var shape thookEventShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return false
	}
	if shape.RewriteType != "" && shape.RewriteType != "*" {
		if string(env.RewriteType) != shape.RewriteType {
			return false
		}
	}
	if shape.TypeID != "" && shape.TypeID != "*" {
		if string(affectedTypeID) != shape.TypeID {
			return false
		}
	}
	if shape.URNPrefix != "" {
		ownerURN := propStr(thook, "owner_urn")
		if !strings.HasPrefix(ownerURN, shape.URNPrefix) {
			return false
		}
	}
	if shape.Port != "" {
		if env.SrcPort != shape.Port && env.TgtPort != shape.Port {
			return false
		}
	}
	if shape.Field != "" && shape.Field != "*" {
		if env.Field != shape.Field {
			return false
		}
	}
	return true
}

// thookGuardPasses evaluates the t_hook's optional guard_ref predicate.
// Returns true if guard_ref is empty (no guard required).
// Fail-closed: returns false if the guard node is referenced but not found.
func (e *Engine) thookGuardPasses(thook graph.Node) bool {
	guardRef := propStr(thook, "guard_ref")
	if guardRef == "" {
		return true
	}
	guardNode, ok := e.State.Nodes[graph.URN(guardRef)]
	if !ok {
		return false // guard missing — fail closed (M8 semantics)
	}
	return e.evaluateGuard(guardNode)
}

// propStr reads a string property value from a node, returning "" if missing.
func propStr(n graph.Node, field string) string {
	p, ok := n.Properties[field]
	if !ok {
		return ""
	}
	s, _ := p.Value.(string)
	return s
}

// propBool reads a boolean property value from a node, returning false if missing.
func propBool(n graph.Node, field string) bool {
	p, ok := n.Properties[field]
	if !ok {
		return false
	}
	b, _ := p.Value.(bool)
	return b
}
