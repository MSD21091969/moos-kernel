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

// Engine evaluates watcher/reactor/guard nodes against incoming rewrites.
// It is read-only over graph state — it never mutates the graph directly.
type Engine struct {
	State *graph.GraphState
}

// Evaluate takes a just-applied rewrite and returns proposed rewrites
// from any matching watcher→reactor chains whose guards all pass.
func (e *Engine) Evaluate(rewrite graph.PersistedRewrite) []graph.Envelope {
	var proposals []graph.Envelope

	env := rewrite.Envelope

	// Determine the affected node URN and type_id from the envelope.
	affectedURN, affectedTypeID := e.affected(env)

	// Scan all nodes for active watchers.
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

// buildFromTemplate parses the reactor's template property as a JSON envelope,
// substituting $matched_urn, $matched_type_id, and $actor placeholders.
func (e *Engine) buildFromTemplate(reactor graph.Node, matchedURN graph.URN, matchedTypeID graph.TypeID, actor graph.URN) (graph.Envelope, error) {
	tmplProp, ok := reactor.Properties["template"]
	if !ok {
		return graph.Envelope{}, fmt.Errorf("reactor %s: no template property", reactor.URN)
	}

	// The template is stored as a JSON object (map or raw JSON).
	raw, err := json.Marshal(tmplProp.Value)
	if err != nil {
		return graph.Envelope{}, fmt.Errorf("reactor %s: marshal template: %w", reactor.URN, err)
	}

	// Substitute placeholders in the raw JSON string.
	s := string(raw)
	s = strings.ReplaceAll(s, "$matched_urn", string(matchedURN))
	s = strings.ReplaceAll(s, "$matched_type_id", string(matchedTypeID))
	s = strings.ReplaceAll(s, "$actor", string(actor))

	var env graph.Envelope
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		return graph.Envelope{}, fmt.Errorf("reactor %s: unmarshal template: %w", reactor.URN, err)
	}

	return env, nil
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
