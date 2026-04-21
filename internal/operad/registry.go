package operad

import "moos/kernel/internal/graph"

// Registry is the operad — the grammar and type system of the kernel.
// It is loaded from ontology.json and is read-only at runtime.
// It governs what rewrites are valid: which node types exist,
// which rewrite categories are allowed, what port colors are compatible.
type Registry struct {
	// Version is the ontology version string (e.g. "3.12.0") parsed from the
	// top-level "version" field of ontology.json. Empty when the registry is
	// built via EmptyRegistry (no ontology loaded). Exposed read-only via
	// /healthz so that state-readback tooling can detect runtime vs. on-disk
	// ontology drift without grepping feature flags.
	Version           string
	NodeTypes         map[graph.TypeID]NodeTypeSpec
	RewriteCategories map[graph.RewriteCategory]RewriteCategorySpec
	PortColorMatrix   PortColorMatrix
}

// NodeTypeSpec describes the valid structure of one node type.
type NodeTypeSpec struct {
	ID          graph.TypeID
	Stratum     string // "S1", "S2", etc.
	URNPattern  string
	Ports       PortSpec
	Properties  map[string]PropertySpec
}

// PortSpec lists the port names for a node type.
type PortSpec struct {
	Out  []string
	In   []string
	Self []string
}

// PropertySpec declares the valid structure of one property on a node type.
type PropertySpec struct {
	Mutability     string // "immutable" | "mutable"
	AuthorityScope string // "kernel" | "owner" | "principal" | "substrate" | "delegate"
	Type           string // "string" | "enum" | "integer" | "datetime" | "urn" | "array" | "object"
	Values         []any  // valid enum values, if type == "enum"
	Note           string
}

// RewriteCategorySpec declares the rules for one WF category.
type RewriteCategorySpec struct {
	ID                   graph.RewriteCategory
	Name                 string
	AllowedRewrites      []graph.RewriteType
	SrcTypes             []graph.TypeID
	TgtTypes             []graph.TypeID
	SrcPort              string
	TgtPort              string
	AdditionalPortPairs  []AdditionalPortPair // v3.10+ extension mechanism (§M19 has-occupant, §M18 pins-urn, etc.)
	Authority            string
	MutateScope          []string // exhaustive list of fields that may be changed under this WF
	SyncMode             string   // "strict" | "eventual" | "local-only"
}

// AdditionalPortPair declares a secondary (src_port, tgt_port) pairing that a WF
// category also accepts in addition to its primary (SrcPort, TgtPort). Introduced
// by the v3.10 D19.1 grammar_fragment to carry WF19 has-occupant/is-occupant-of
// topology without bumping to a new WF number, then extended by v3.12 (D19.3
// pins-urn, D19.4 filtered-by, D20.1 mounts-tool) for §M18-§M20 workspace shape.
//
// Loader consumes these into the registry so ValidateLINK can accept any
// declared pair for the WF. SrcTypes/TgtTypes, when non-empty, further restrict
// the pair to specific node types — empty means the WF's top-level lists apply.
type AdditionalPortPair struct {
	SrcPort          string
	TgtPort          string
	SrcTypes         []graph.TypeID
	TgtTypes         []graph.TypeID
	AddedInVersion   string // e.g. "3.10.0"
	PromotesFragment string // URN of the grammar_fragment this pair promotes from, if any
	Description      string
}

// PortColorMatrix is the §12.2 compatibility matrix.
// compat[srcColor][tgtColor] = true means a LINK rewrite is color-valid.
// "wf15_only" = only allowed with WF15 + contract_urn.
// "sink_only" = projection sink, no truth-carrying relation produced.
type PortColorMatrix map[graph.PortColor]map[graph.PortColor]colorCompat

type colorCompat string

const (
	compatAllowed  colorCompat = "true"
	compatWF15Only colorCompat = "wf15_only"
	compatSinkOnly colorCompat = "sink_only"
	compatFalse    colorCompat = "false"
)

// Allowed returns true if a LINK from srcColor port to tgtColor port is valid
// under the given rewrite category.
func (m PortColorMatrix) Allowed(src, tgt graph.PortColor, wf graph.RewriteCategory) bool {
	row, ok := m[src]
	if !ok {
		return false
	}
	switch row[tgt] {
	case compatAllowed:
		return true
	case compatWF15Only:
		return wf == graph.WF15
	default:
		return false
	}
}

// Empty registry for testing or startup before ontology is loaded.
func EmptyRegistry() *Registry {
	return &Registry{
		NodeTypes:        make(map[graph.TypeID]NodeTypeSpec),
		RewriteCategories: make(map[graph.RewriteCategory]RewriteCategorySpec),
		PortColorMatrix:  make(PortColorMatrix),
	}
}
