package graph

import "fmt"

// Stratum marks the layer of provenance for a node or property value.
// Every node and every property records its stratum origin.
// S4 (Projected) is never source of truth — it may not be promoted
// without explicit governance (WF13).
type Stratum int

const (
	S0 Stratum = iota // Authored   — human-written seeds, design docs
	S1                // Validated  — operad-checked, type-approved
	S2                // Materialized — hydrated into kernel graph, operational
	S3                // Evaluated  — concurrent subgraph eval, hypervector encoding
	S4                // Projected  — UI, filesystem, API — NEVER ground truth
)

func (s Stratum) String() string {
	switch s {
	case S0:
		return "S0"
	case S1:
		return "S1"
	case S2:
		return "S2"
	case S3:
		return "S3"
	case S4:
		return "S4"
	default:
		return fmt.Sprintf("S?(%d)", int(s))
	}
}

func ParseStratum(s string) (Stratum, error) {
	switch s {
	case "S0", "s0":
		return S0, nil
	case "S1", "s1":
		return S1, nil
	case "S2", "s2":
		return S2, nil
	case "S3", "s3":
		return S3, nil
	case "S4", "s4":
		return S4, nil
	default:
		return S0, fmt.Errorf("unknown stratum %q", s)
	}
}
