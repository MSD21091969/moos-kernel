package operad

import (
	"encoding/json"
	"fmt"
	"os"

	"moos/kernel/internal/graph"
)

// LoadRegistry parses ontology.json and builds a Registry. The ontology
// path should point to ffs0/kb/superset/ontology.json or a copy of it.
// The parsed ontology version is captured on the returned Registry's
// Version field; callers that care about schema pinning should read that
// rather than assume a version here.
//
// If path is empty, returns an EmptyRegistry with a warning — the kernel
// will run but without type validation.
func LoadRegistry(path string) (*Registry, error) {
	if path == "" {
		return EmptyRegistry(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("operad: read ontology %q: %w", path, err)
	}

	var raw ontologyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("operad: parse ontology %q: %w", path, err)
	}

	reg := EmptyRegistry()
	reg.Version = raw.Version

	// Load node types from s2_infrastructure
	for _, nt := range raw.Types.S2Infrastructure {
		reg.NodeTypes[graph.TypeID(nt.ID)] = parseNodeTypeSpec(nt)
	}
	// Load s1_grammar types
	for _, nt := range raw.Types.S1Grammar {
		reg.NodeTypes[graph.TypeID(nt.ID)] = parseNodeTypeSpec(nt)
	}
	// Load interaction nodes
	for _, nt := range raw.Types.InteractionNodes {
		reg.NodeTypes[graph.TypeID(nt.ID)] = parseNodeTypeSpec(nt)
	}

	// Load rewrite categories (WF01-WF19)
	for _, wf := range raw.RewriteCategories {
		spec := RewriteCategorySpec{
			ID:       graph.RewriteCategory(wf.ID),
			Name:     wf.Name,
			SrcPort:  wf.SrcPort,
			TgtPort:  wf.TgtPort,
			Authority: wf.Authority,
			SyncMode: wf.SyncMode,
		}
		for _, rt := range wf.AllowedRewrites {
			spec.AllowedRewrites = append(spec.AllowedRewrites, graph.RewriteType(rt))
		}
		for _, t := range wf.SrcTypes {
			spec.SrcTypes = append(spec.SrcTypes, graph.TypeID(t))
		}
		for _, t := range wf.TgtTypes {
			spec.TgtTypes = append(spec.TgtTypes, graph.TypeID(t))
		}
		if ms, ok := wf.MutateScope.([]any); ok {
			for _, s := range ms {
				if sv, ok := s.(string); ok {
					spec.MutateScope = append(spec.MutateScope, sv)
				}
			}
		}
		reg.RewriteCategories[spec.ID] = spec
	}

	// Load port color compatibility matrix
	if raw.PortColorCompatibility.Matrix != nil {
		reg.PortColorMatrix = parseColorMatrix(raw.PortColorCompatibility.Matrix)
	}

	return reg, nil
}

func parseNodeTypeSpec(raw rawNodeType) NodeTypeSpec {
	spec := NodeTypeSpec{
		ID:         graph.TypeID(raw.ID),
		Stratum:    raw.Stratum,
		URNPattern: raw.URNPattern,
	}
	if raw.Ports != nil {
		spec.Ports = PortSpec{
			Out:  raw.Ports.Out,
			In:   raw.Ports.In,
			Self: raw.Ports.Self,
		}
	}
	spec.Properties = make(map[string]PropertySpec, len(raw.Properties))
	for name, p := range raw.Properties {
		ps := PropertySpec{
			Mutability:     p.Mutability,
			AuthorityScope: p.AuthorityScope,
			Type:           p.Type,
			Note:           p.Note,
		}
		for _, v := range p.Values {
			ps.Values = append(ps.Values, v)
		}
		spec.Properties[name] = ps
	}
	return spec
}

func parseColorMatrix(raw map[string]map[string]any) PortColorMatrix {
	m := make(PortColorMatrix)
	for src, row := range raw {
		srcColor := graph.PortColor(src)
		m[srcColor] = make(map[graph.PortColor]colorCompat)
		for tgt, val := range row {
			tgtColor := graph.PortColor(tgt)
			switch v := val.(type) {
			case bool:
				if v {
					m[srcColor][tgtColor] = compatAllowed
				} else {
					m[srcColor][tgtColor] = compatFalse
				}
			case string:
				switch v {
				case "wf15_only":
					m[srcColor][tgtColor] = compatWF15Only
				case "sink_only":
					m[srcColor][tgtColor] = compatSinkOnly
				default:
					m[srcColor][tgtColor] = compatFalse
				}
			}
		}
	}
	return m
}

// --- raw JSON shapes for ontology.json ---

type ontologyJSON struct {
	Version string `json:"version"`
	Types   struct {
		S2Infrastructure []rawNodeType `json:"s2_infrastructure"`
		S1Grammar        []rawNodeType `json:"s1_grammar"`
		InteractionNodes []rawNodeType `json:"interaction_nodes"`
	} `json:"types"`
	RewriteCategories      []rawRewriteCategory `json:"rewrite_categories"`
	PortColorCompatibility rawPortColorCompat   `json:"port_color_compatibility"`
}

type rawNodeType struct {
	ID         string                        `json:"id"`
	Stratum    string                        `json:"stratum"`
	URNPattern string                        `json:"urn_pattern"`
	Ports      *rawPorts                     `json:"ports"`
	Properties map[string]rawPropertySpec    `json:"properties"`
}

type rawPorts struct {
	Out  []string `json:"out"`
	In   []string `json:"in"`
	Self []string `json:"self"`
}

type rawPropertySpec struct {
	Mutability     string `json:"mutability"`
	AuthorityScope string `json:"authority_scope"`
	Type           string `json:"type"`
	Values         []any  `json:"values"`
	Note           string `json:"note"`
}

type rawRewriteCategory struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	AllowedRewrites []string `json:"allowed_rewrites"`
	SrcTypes        []string `json:"src_types"`
	TgtTypes        []string `json:"tgt_types"`
	SrcPort         string   `json:"src_port"`
	TgtPort         string   `json:"tgt_port"`
	Authority       string   `json:"authority"`
	MutateScope     any      `json:"mutate_scope"` // []string or null or string
	SyncMode        string   `json:"sync_mode"`
}

type rawPortColorCompat struct {
	Matrix map[string]map[string]any `json:"matrix"`
}
