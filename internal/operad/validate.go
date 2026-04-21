package operad

import (
	"fmt"

	"moos/kernel/internal/graph"
)

// ValidateADD checks structural constraints on an ADD envelope.
// Returns nil if the ADD is valid given the registry.
func (r *Registry) ValidateADD(env graph.Envelope) error {
	spec, ok := r.NodeTypes[env.TypeID]
	if !ok {
		return fmt.Errorf("operad: unknown type_id %q", env.TypeID)
	}

	// Check all required (non-optional) properties are present
	for name, pspec := range spec.Properties {
		if _, provided := env.Properties[name]; !provided {
			// Only enforce for immutable fields — they must be set at ADD time
			if pspec.Mutability == "immutable" {
				return fmt.Errorf("operad: required immutable property %q missing on ADD of %s", name, env.TypeID)
			}
		}
	}
	return nil
}

// ValidateLINK checks port, type, and color constraints on a LINK envelope.
//
// Port-pair validation (T=171 round 11 PR 1): the (src_port, tgt_port) pair
// must match either the WF's primary (SrcPort, TgtPort) or one of its declared
// AdditionalPortPairs. Pairs declared only on the AdditionalPortPairs list of
// ontology.json (e.g. WF19 has-occupant/is-occupant-of, pins-urn/pinned-by-session)
// are now validated — previously they slipped through when the loader didn't
// consume the field. Prospective only: replay of pre-PR-1 logs via fold runs
// without re-validation, so historical non-canonical relations (e.g. an LINK
// recorded at v3.11 with a typo'd tgt_port) persist in state. Correction of
// such relations is via new compensating UNLINK+re-LINK rewrites, not via
// retroactive validator action.
func (r *Registry) ValidateLINK(env graph.Envelope) error {
	wfSpec, ok := r.RewriteCategories[env.RewriteCategory]
	if !ok {
		return fmt.Errorf("operad: unknown rewrite_category %q", env.RewriteCategory)
	}

	// Check allowed_rewrites includes LINK
	if !containsRewrite(wfSpec.AllowedRewrites, graph.LINK) {
		return fmt.Errorf("operad: rewrite_category %s does not allow LINK", env.RewriteCategory)
	}

	// WF15 contract requirement is checked in fold — but double-check here as well
	if env.RewriteCategory == graph.WF15 && env.ContractURN == "" {
		return fmt.Errorf("operad: WF15 LINK requires contract_urn")
	}

	// Port pair must match one of the WF's declared pairs (primary or additional).
	// Only enforced when the WF actually declares a primary pair; empty means the
	// WF is a degenerate or spec-absent category — keep the old permissive
	// behavior in that case.
	if wfSpec.SrcPort != "" || wfSpec.TgtPort != "" || len(wfSpec.AdditionalPortPairs) > 0 {
		if !linkPairDeclared(wfSpec, env.SrcPort, env.TgtPort) {
			return fmt.Errorf("operad: port pair (%s, %s) not declared for %s; expected %s",
				env.SrcPort, env.TgtPort, env.RewriteCategory,
				describeDeclaredPairs(wfSpec))
		}
	}

	// Port color compatibility (§12.2). Unknown colors remain permissive so
	// registry gaps don't hard-block rewrites; strict pair validation above
	// already catches typo'd ports.
	srcColor, tgtColor, err := r.resolvePortColors(env.SrcPort, env.TgtPort)
	if err != nil {
		return nil
	}
	if !r.PortColorMatrix.Allowed(srcColor, tgtColor, env.RewriteCategory) {
		return fmt.Errorf("operad: port color incompatibility: %s → %s not allowed under %s (§12.2)",
			srcColor, tgtColor, env.RewriteCategory)
	}
	return nil
}

// linkPairDeclared reports whether (src, tgt) matches the WF's primary pair or
// any of its additional_port_pairs.
func linkPairDeclared(wfSpec RewriteCategorySpec, src, tgt string) bool {
	if wfSpec.SrcPort == src && wfSpec.TgtPort == tgt {
		return true
	}
	for _, p := range wfSpec.AdditionalPortPairs {
		if p.SrcPort == src && p.TgtPort == tgt {
			return true
		}
	}
	return false
}

// describeDeclaredPairs renders the WF's accepted port pairs for error messages.
// Primary pair first, additional pairs in declaration order.
func describeDeclaredPairs(wfSpec RewriteCategorySpec) string {
	pairs := make([]string, 0, 1+len(wfSpec.AdditionalPortPairs))
	if wfSpec.SrcPort != "" || wfSpec.TgtPort != "" {
		pairs = append(pairs, fmt.Sprintf("(%s, %s)", wfSpec.SrcPort, wfSpec.TgtPort))
	}
	for _, p := range wfSpec.AdditionalPortPairs {
		pairs = append(pairs, fmt.Sprintf("(%s, %s)", p.SrcPort, p.TgtPort))
	}
	if len(pairs) == 0 {
		return "(none declared)"
	}
	out := pairs[0]
	for _, p := range pairs[1:] {
		out += " | " + p
	}
	return out
}

// ValidateMUTATE checks mutability, WF scope, and authority constraints on a MUTATE envelope.
//
// Two paths:
//  1. Standard MUTATE: field already on node — full WF validation (rewrite_category required, field must be in mutate_scope).
//  2. Additive MUTATE: field not yet on node — validate against ontology type spec only.
//     WF mutate_scope is skipped because the field is being added for the first time (e.g. a new
//     optional property from a later ontology version). rewrite_category may be empty.
func (r *Registry) ValidateMUTATE(env graph.Envelope, node graph.Node) error {
	_, fieldOnNode := node.Properties[env.Field]

	if !fieldOnNode {
		// Additive MUTATE: field not on node — validate via ontology type spec only.
		typeSpec, hasTypeSpec := r.NodeTypes[node.TypeID]
		if hasTypeSpec {
			pspec, hasPspec := typeSpec.Properties[env.Field]
			if !hasPspec {
				return fmt.Errorf("operad: field %q not declared in type spec for %s (additive MUTATE requires ontology-declared field)", env.Field, node.TypeID)
			}
			if pspec.Mutability != "mutable" {
				return fmt.Errorf("operad: field %q is not mutable in type spec for %s", env.Field, node.TypeID)
			}
			if err := checkAuthority(pspec.AuthorityScope, env.Actor, node); err != nil {
				return err
			}
		}
		// If a rewrite_category is provided, it must be a known one — but it's optional here.
		if env.RewriteCategory != "" {
			if _, ok := r.RewriteCategories[env.RewriteCategory]; !ok {
				return fmt.Errorf("operad: unknown rewrite_category %q", env.RewriteCategory)
			}
		}
		return nil
	}

	// Standard MUTATE: field already on node — full WF validation.
	wfSpec, ok := r.RewriteCategories[env.RewriteCategory]
	if !ok {
		return fmt.Errorf("operad: unknown rewrite_category %q", env.RewriteCategory)
	}

	if !containsRewrite(wfSpec.AllowedRewrites, graph.MUTATE) {
		return fmt.Errorf("operad: rewrite_category %s does not allow MUTATE", env.RewriteCategory)
	}

	// Field must be in the WF's exhaustive mutate_scope
	if len(wfSpec.MutateScope) > 0 && !containsString(wfSpec.MutateScope, env.Field) {
		return fmt.Errorf("operad: field %q not in mutate_scope for %s (§5)", env.Field, env.RewriteCategory)
	}

	// Check the property spec for authority_scope
	typeSpec, hasTypeSpec := r.NodeTypes[node.TypeID]
	if hasTypeSpec {
		if pspec, hasProp := typeSpec.Properties[env.Field]; hasProp {
			if pspec.Mutability == "immutable" {
				return fmt.Errorf("operad: field %q is immutable on type %s", env.Field, node.TypeID)
			}
			// Authority check: actor URN prefix must match authority scope
			if err := checkAuthority(pspec.AuthorityScope, env.Actor, node); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateUNLINK checks the UNLINK envelope against the registry.
func (r *Registry) ValidateUNLINK(env graph.Envelope) error {
	if env.RewriteCategory == "" {
		return nil // UNLINK without category is allowed (category resolved from existing relation)
	}
	wfSpec, ok := r.RewriteCategories[env.RewriteCategory]
	if !ok {
		return fmt.Errorf("operad: unknown rewrite_category %q", env.RewriteCategory)
	}
	if !containsRewrite(wfSpec.AllowedRewrites, graph.UNLINK) {
		return fmt.Errorf("operad: rewrite_category %s does not allow UNLINK", env.RewriteCategory)
	}
	return nil
}

// resolvePortColors looks up port colors from the registry's declared pairs.
// Returns an error if either port is unknown.
func (r *Registry) resolvePortColors(srcPort, tgtPort string) (graph.PortColor, graph.PortColor, error) {
	srcColor := portColorFromName(srcPort)
	tgtColor := portColorFromName(tgtPort)
	if srcColor == "" || tgtColor == "" {
		return "", "", fmt.Errorf("unknown port color for %q or %q", srcPort, tgtPort)
	}
	return srcColor, tgtColor, nil
}

// portColorFromName maps well-known port names to their canonical color (§12.1).
func portColorFromName(port string) graph.PortColor {
	switch port {
	case "governs", "governed-by", "granted-by", "identity", "promotes-to", "promotion-target":
		return graph.ColorAuth
	case "owns", "child", "hosts", "hosted-on", "contains", "contained-in", "binds":
		return graph.ColorTopology
	case "exposes", "exposed-by", "connects-to", "connected-to", "implements", "implemented-by",
		"routes-to", "routed-from", "shard-of", "sharded-by":
		return graph.ColorTransport
	case "computes-on", "computed-by":
		return graph.ColorCompute
	case "persisted-in", "persists", "synced-via", "sync-target", "provides-kb", "kb-source",
		"produces", "produced-by", "asserts", "asserted-in", "tagged", "tagged-in":
		return graph.ColorStorage
	case "bound-to":
		return "" // ambiguous: compute or storage depending on src node type — skip color check
	case "participates", "participated-by", "focus", "on",
		"anchors", "anchor", "causes", "summarizes", "daily-summary", "depends-on", "depended-by", "participant",
		"triggers", "triggered-by", "guards", "guarded-by", "emits", "emitted-by", "watches", "watched-by",
		"has-occupant", "is-occupant-of", // v3.10 WF19 session-occupancy port pair (§M19)
		"pins-urn", "pinned-by-session", // v3.12 WF19 session pins (§M18, D19.3)
		"filtered-by", "filters-session", // v3.12 WF19 session filter binding (§M18, D19.4)
		"mounts-tool", "tool-mounted-in-session": // v3.12 WF19 session tool mount (§M20, D20.1)
		return graph.ColorWorkflow
	case "projected-to", "rendered-as", "displayed-as":
		return graph.ColorProjection
	}
	return "" // unknown port
}

// checkAuthority checks whether the actor satisfies the required authority scope.
// This is a heuristic check — full capability-graph checking is done at runtime.
func checkAuthority(scope string, actor graph.URN, node graph.Node) error {
	switch scope {
	case "kernel":
		// Kernel authority: actor must be a kernel URN
		// (urn:moos:kernel:...) — relaxed here, full check at runtime
		return nil
	case "owner":
		// Owner authority: actor must match owner_urn property of the node
		if ownerProp, ok := node.Properties["owner_urn"]; ok {
			if ownerURN, ok := ownerProp.Value.(string); ok {
				if graph.URN(ownerURN) != actor {
					return fmt.Errorf("operad: field requires owner authority; actor %s != owner %s", actor, ownerURN)
				}
			}
		}
	case "principal", "substrate", "delegate":
		// These require capability graph traversal — deferred to runtime capability check
		return nil
	}
	return nil
}

func containsRewrite(list []graph.RewriteType, rt graph.RewriteType) bool {
	for _, r := range list {
		if r == rt {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ValidateStrataLink enforces M5 strata filtration for LINK rewrites.
// Called with the kernel read-lock held (state is consistent).
//
// Rules:
//  1. S4 (Projected) nodes may not be the src of a LINK to S0/S1/S2 nodes.
//     Projection nodes are read-only toward lower strata (M5).
//  2. If the WF category declares non-empty src_types or tgt_types, the actual
//     node types must be in those lists. (Previously stubbed — now enforced.)
func (r *Registry) ValidateStrataLink(env graph.Envelope, state graph.GraphState) error {
	srcNode, srcOk := state.Nodes[env.SrcURN]
	tgtNode, tgtOk := state.Nodes[env.TgtURN]
	if !srcOk || !tgtOk {
		return nil // nodes not found — fold.applyLINK will return ErrNodeNotFound
	}

	srcSpec, hasSrc := r.NodeTypes[srcNode.TypeID]
	tgtSpec, hasTgt := r.NodeTypes[tgtNode.TypeID]
	if !hasSrc || !hasTgt {
		return nil // unknown type — already rejected by ValidateADD at creation time
	}

	srcStratum, err := graph.ParseStratum(srcSpec.Stratum)
	if err != nil {
		return nil // unknown stratum — be permissive; operad coverage issue, not a violation
	}
	tgtStratum, err := graph.ParseStratum(tgtSpec.Stratum)
	if err != nil {
		return nil
	}

	// Rule 1: S4 → S0/S1/S2 LINK is forbidden (M5 filtration direction).
	if srcStratum == graph.S4 && tgtStratum < graph.S3 {
		return fmt.Errorf("strata(M5): S4 node %s (%s) may not LINK to %s node %s (%s); projection nodes are read-only toward lower strata",
			env.SrcURN, srcNode.TypeID, tgtSpec.Stratum, env.TgtURN, tgtNode.TypeID)
	}

	// Rule 2: WF src_types / tgt_types enforcement.
	wfSpec, ok := r.RewriteCategories[env.RewriteCategory]
	if !ok {
		return nil // unknown WF — already caught by ValidateLINK
	}
	if len(wfSpec.SrcTypes) > 0 && !containsTypeID(wfSpec.SrcTypes, srcNode.TypeID) {
		return fmt.Errorf("operad: src type %q not in allowed list for %s: %v",
			srcNode.TypeID, env.RewriteCategory, wfSpec.SrcTypes)
	}
	if len(wfSpec.TgtTypes) > 0 && !containsTypeID(wfSpec.TgtTypes, tgtNode.TypeID) {
		return fmt.Errorf("operad: tgt type %q not in allowed list for %s: %v",
			tgtNode.TypeID, env.RewriteCategory, wfSpec.TgtTypes)
	}
	return nil
}

func containsTypeID(list []graph.TypeID, id graph.TypeID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}
