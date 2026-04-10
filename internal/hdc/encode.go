package hdc

import (
	"strings"

	"moos/kernel/internal/graph"
)

var defaultEncoder = NewEncoder()

const typeWeight = 6

var (
	roleSrcURN  = graph.URN("urn:moos:hdc:role:src")
	roleTgtURN  = graph.URN("urn:moos:hdc:role:tgt")
	roleWfURN   = graph.URN("urn:moos:hdc:role:wf")
	roleSPort   = graph.URN("urn:moos:hdc:role:src_port")
	roleTPort   = graph.URN("urn:moos:hdc:role:tgt_port")
	roleTypeURN = graph.URN("urn:moos:hdc:role:type")
	roleSelfURN = graph.URN("urn:moos:hdc:role:self")
	dirInURN    = graph.URN("urn:moos:hdc:dir:in")
	dirOutURN   = graph.URN("urn:moos:hdc:dir:out")
)

// Encoder holds a codebook used for graded encodings.
type Encoder struct {
	Codebook Codebook
}

func NewEncoder() *Encoder {
	return &Encoder{Codebook: NewCodebook()}
}

// EncodeRelation encodes one relation (grade 1) using the default encoder.
func EncodeRelation(rel graph.Relation) HV {
	return defaultEncoder.EncodeRelation(rel)
}

// EncodeNode encodes one node with incident relations (grade 2) using the default encoder.
func EncodeNode(state graph.GraphState, urn graph.URN) HV {
	return defaultEncoder.EncodeNode(state, urn)
}

// EncodeRelation encodes one relation (grade 1).
func (e *Encoder) EncodeRelation(rel graph.Relation) HV {
	cb := e.Codebook

	src := Bind(cb.Encode(roleSrcURN), cb.Encode(rel.SrcURN))
	tgt := Bind(cb.Encode(roleTgtURN), cb.Encode(rel.TgtURN))
	wf := Bind(cb.Encode(roleWfURN), cb.Encode(tokenURN("wf", string(rel.RewriteCategory))))
	sPort := Bind(cb.Encode(roleSPort), cb.Encode(tokenURN("port", rel.SrcPort)))
	tPort := Bind(cb.Encode(roleTPort), cb.Encode(tokenURN("port", rel.TgtPort)))

	return Bundle(src, tgt, wf, sPort, tPort)
}

// EncodeNode encodes one node (grade 2) by bundling type, self, and incident wires.
func (e *Encoder) EncodeNode(state graph.GraphState, urn graph.URN) HV {
	node, ok := state.Nodes[urn]
	if !ok {
		return HV{}
	}

	cb := e.Codebook
	typeVec := Bind(cb.Encode(roleTypeURN), cb.Encode(tokenURN("type", string(node.TypeID))))
	selfVec := Bind(cb.Encode(roleSelfURN), cb.Encode(urn))

	vectors := make([]HV, 0, typeWeight+1+len(state.Relations))
	for i := 0; i < typeWeight; i++ {
		vectors = append(vectors, typeVec)
	}
	vectors = append(vectors, selfVec)

	for _, rel := range state.Relations {
		if rel.SrcURN != urn && rel.TgtURN != urn {
			continue
		}
		wire := e.EncodeRelation(rel)
		if rel.SrcURN == urn {
			wire = Bind(cb.Encode(dirOutURN), wire)
		} else {
			wire = Bind(cb.Encode(dirInURN), wire)
		}
		vectors = append(vectors, wire)
	}

	return Bundle(vectors...)
}

func (e *Encoder) EncodeNodes(state graph.GraphState, urns []graph.URN) map[graph.URN]HV {
	adj := make(map[graph.URN][]graph.Relation)
	for _, rel := range state.Relations {
		adj[rel.SrcURN] = append(adj[rel.SrcURN], rel)
		if rel.SrcURN != rel.TgtURN {
			adj[rel.TgtURN] = append(adj[rel.TgtURN], rel)
		}
	}

	cb := e.Codebook
	out := make(map[graph.URN]HV, len(urns))

	for _, urn := range urns {
		node, ok := state.Nodes[urn]
		if !ok {
			out[urn] = HV{}
			continue
		}

		typeVec := Bind(cb.Encode(roleTypeURN), cb.Encode(tokenURN("type", string(node.TypeID))))
		selfVec := Bind(cb.Encode(roleSelfURN), cb.Encode(urn))

		rels := adj[urn]
		vectors := make([]HV, 0, typeWeight+1+len(rels))
		for i := 0; i < typeWeight; i++ {
			vectors = append(vectors, typeVec)
		}
		vectors = append(vectors, selfVec)

		for _, rel := range rels {
			wire := e.EncodeRelation(rel)
			if rel.SrcURN == urn {
				wire = Bind(cb.Encode(dirOutURN), wire)
			} else {
				wire = Bind(cb.Encode(dirInURN), wire)
			}
			vectors = append(vectors, wire)
		}

		out[urn] = Bundle(vectors...)
	}

	return out
}

func tokenURN(prefix, value string) graph.URN {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		v = "empty"
	}
	repl := strings.NewReplacer(" ", "_", "/", "_", ":", "_", ".", "_", "-", "_")
	v = repl.Replace(v)
	return graph.URN("urn:moos:hdc:" + prefix + ":" + v)
}
