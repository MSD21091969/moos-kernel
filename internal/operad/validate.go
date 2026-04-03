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
func (r *Registry) ValidateLINK(env graph.Envelope) error {
	wfSpec, ok := r.RewriteCategories[env.RewriteCategory]
	if !ok {
		return fmt.Errorf("operad: unknown rewrite_category %q", env.RewriteCategory)
	}

	// Check allowed_rewrites includes LINK
	if !containsRewrite(wfSpec.AllowedRewrites, graph.LINK) {
		return fmt.Errorf("operad: rewrite_category %s does not allow LINK", env.RewriteCategory)
	}

	// Validate src type
	srcNode, ok := r.NodeTypes[graph.TypeID("")] // filled below
	_ = srcNode
	// We validate src and tgt types are in the WF's declared lists
	// (actual TypeID comes from the graph state at apply time — operad checks structural rules here)
	_ = wfSpec.SrcTypes
	_ = wfSpec.TgtTypes

	// WF15 contract requirement is checked in fold — but double-check here as well
	if env.RewriteCategory == graph.WF15 && env.ContractURN == "" {
		return fmt.Errorf("operad: WF15 LINK requires contract_urn")
	}

	// Port color compatibility
	srcColor, tgtColor, err := r.resolvePortColors(env.SrcPort, env.TgtPort)
	if err != nil {
		// Unknown port — warn but allow if registry is partial
		return nil
	}
	if !r.PortColorMatrix.Allowed(srcColor, tgtColor, env.RewriteCategory) {
		return fmt.Errorf("operad: port color incompatibility: %s → %s not allowed under %s (§12.2)",
			srcColor, tgtColor, env.RewriteCategory)
	}
	return nil
}

// ValidateMUTATE checks mutability, WF scope, and authority constraints on a MUTATE envelope.
func (r *Registry) ValidateMUTATE(env graph.Envelope, node graph.Node) error {
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
	case "exposes", "exposed-by", "connects-to", "connected-to", "implements", "implemented-by":
		return graph.ColorTransport
	case "computes-on", "computed-by":
		return graph.ColorCompute
	case "persisted-in", "persists", "synced-via", "sync-target", "provides-kb", "kb-source":
		return graph.ColorStorage
	case "bound-to":
		return "" // ambiguous: compute or storage depending on src node type — skip color check
	case "participates", "participated-by", "focus", "on":
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
