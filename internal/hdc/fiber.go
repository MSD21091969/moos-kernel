package hdc

import (
	"math"
	"sort"
	"strings"

	"moos/kernel/internal/graph"
)

// FiberAssignmentEntry reports current and optimal kernel placement for one node.
type FiberAssignmentEntry struct {
	URN           graph.URN `json:"urn"`
	CurrentKernel graph.URN `json:"current_kernel"`
	OptimalKernel graph.URN `json:"optimal_kernel"`
	Distance      float64   `json:"distance"`
}

// FiberDistributionEntry reports per-kernel type distribution and divergence to federation baseline.
type FiberDistributionEntry struct {
	Kernel          graph.URN                `json:"kernel"`
	TypeHistogram   map[graph.TypeID]float64 `json:"type_histogram"`
	JSDToFederation float64                  `json:"jsd_to_federation"`
}

// FiberVectorResponse is returned by /hdc/fiber.
type FiberVectorResponse struct {
	Kernel    graph.URN `json:"kernel"`
	NodeCount int       `json:"node_count"`
	Vector    []float32 `json:"vector"`
}

// FederationVectorResponse is returned by /hdc/federation.
type FederationVectorResponse struct {
	KernelCount int       `json:"kernel_count"`
	Vector      []float32 `json:"vector"`
}

// KernelURNs returns sorted kernel node URNs in the graph.
func KernelURNs(state graph.GraphState) []graph.URN {
	out := make([]graph.URN, 0)
	for urn, n := range state.Nodes {
		if n.TypeID == "kernel" {
			out = append(out, urn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// EncodeFiber computes a grade-3 hypervector for one kernel fiber.
func EncodeFiber(state graph.GraphState, kernelURN graph.URN, enc *Encoder) HV {
	if enc == nil {
		enc = NewEncoder()
	}
	nodeURNs := NodesInKernel(state, kernelURN)
	vectors := make([]HV, 0, len(nodeURNs))
	
	encodedBatch := enc.EncodeNodes(state, nodeURNs)
	for _, urn := range nodeURNs {
		vectors = append(vectors, encodedBatch[urn])
	}
	return Bundle(vectors...)
}

// EncodeFederation computes a grade-4 federation hypervector from fiber vectors.
func EncodeFederation(fibers map[graph.URN]HV) HV {
	if len(fibers) == 0 {
		return HV{}
	}
	keys := make([]graph.URN, 0, len(fibers))
	for k := range fibers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	vectors := make([]HV, 0, len(keys))
	for _, k := range keys {
		vectors = append(vectors, fibers[k])
	}
	return Bundle(vectors...)
}

// FiberVectorsByKernel computes all kernel fiber vectors.
func FiberVectorsByKernel(state graph.GraphState, enc *Encoder) map[graph.URN]HV {
	kernels := KernelURNs(state)
	out := make(map[graph.URN]HV, len(kernels))
	for _, k := range kernels {
		out[k] = EncodeFiber(state, k, enc)
	}
	return out
}

// NodesInKernel returns sorted node URNs currently inferred to belong to kernelURN.
func NodesInKernel(state graph.GraphState, kernelURN graph.URN) []graph.URN {
	kernels := KernelURNs(state)
	out := make([]graph.URN, 0)
	for urn, node := range state.Nodes {
		if inferCurrentKernel(state, kernels, urn, node) == kernelURN {
			out = append(out, urn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FiberAssignments computes current vs. optimal kernel placement for each node.
func FiberAssignments(state graph.GraphState, enc *Encoder) []FiberAssignmentEntry {
	if enc == nil {
		enc = NewEncoder()
	}

	kernels := KernelURNs(state)
	if len(kernels) == 0 {
		return nil
	}
	fibers := FiberVectorsByKernel(state, enc)

	urns := make([]graph.URN, 0, len(state.Nodes))
	for urn := range state.Nodes {
		urns = append(urns, urn)
	}
	sort.Slice(urns, func(i, j int) bool { return urns[i] < urns[j] })

	out := make([]FiberAssignmentEntry, 0, len(urns))
	encodedBatch := enc.EncodeNodes(state, urns)
	for _, urn := range urns {
		node := state.Nodes[urn]
		current := inferCurrentKernel(state, kernels, urn, node)
		nodeVec := encodedBatch[urn]

		bestKernel := kernels[0]
		bestDistance := l2Distance(nodeVec, fibers[bestKernel])
		for _, k := range kernels[1:] {
			d := l2Distance(nodeVec, fibers[k])
			if d < bestDistance {
				bestDistance = d
				bestKernel = k
			}
		}

		out = append(out, FiberAssignmentEntry{
			URN:           urn,
			CurrentKernel: current,
			OptimalKernel: bestKernel,
			Distance:      bestDistance,
		})
	}

	return out
}

// FiberDistribution computes per-kernel type histograms and JSD to federation type distribution.
func FiberDistribution(state graph.GraphState) []FiberDistributionEntry {
	kernels := KernelURNs(state)
	if len(kernels) == 0 {
		return nil
	}

	federationCounts := make(map[graph.TypeID]float64)
	for _, node := range state.Nodes {
		federationCounts[node.TypeID] += 1
	}
	federationDist := normalizeDistribution(federationCounts)

	out := make([]FiberDistributionEntry, 0, len(kernels))
	for _, k := range kernels {
		counts := make(map[graph.TypeID]float64)
		for urn, node := range state.Nodes {
			if inferCurrentKernel(state, kernels, urn, node) == k {
				counts[node.TypeID] += 1
			}
		}
		dist := normalizeDistribution(counts)
		out = append(out, FiberDistributionEntry{
			Kernel:          k,
			TypeHistogram:   dist,
			JSDToFederation: JensenShannonDivergence(dist, federationDist),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Kernel < out[j].Kernel })
	return out
}

// JensenShannonDivergence computes JSD(P || Q) with log base 2, bounded in [0,1].
func JensenShannonDivergence(p, q map[graph.TypeID]float64) float64 {
	keys := make(map[graph.TypeID]struct{})
	for k := range p {
		keys[k] = struct{}{}
	}
	for k := range q {
		keys[k] = struct{}{}
	}

	m := make(map[graph.TypeID]float64, len(keys))
	for k := range keys {
		m[k] = 0.5 * (p[k] + q[k])
	}

	return 0.5*klDivergence(p, m) + 0.5*klDivergence(q, m)
}

func klDivergence(p, q map[graph.TypeID]float64) float64 {
	var out float64
	for k, pv := range p {
		if pv <= 0 {
			continue
		}
		qv := q[k]
		if qv <= 0 {
			continue
		}
		out += pv * (math.Log(pv/qv) / math.Log(2))
	}
	return out
}

func normalizeDistribution(counts map[graph.TypeID]float64) map[graph.TypeID]float64 {
	out := make(map[graph.TypeID]float64, len(counts))
	var sum float64
	for _, v := range counts {
		sum += v
	}
	if sum == 0 {
		return out
	}
	for k, v := range counts {
		out[k] = v / sum
	}
	return out
}

func inferCurrentKernel(state graph.GraphState, kernels []graph.URN, nodeURN graph.URN, node graph.Node) graph.URN {
	if len(kernels) == 0 {
		return ""
	}

	for _, k := range kernels {
		if nodeURN == k {
			return k
		}
	}

	urnText := nodeURN.String()
	for _, k := range kernels {
		kText := k.String()
		if strings.HasPrefix(urnText, kText+":") || strings.HasPrefix(urnText, kText+".") {
			return k
		}
	}

	for _, k := range kernels {
		alias := kernelAlias(k)
		if alias == "" || alias == "primary" {
			continue
		}
		if hasAliasSegment(urnText, alias) {
			return k
		}
	}

	prefixMap := shardPrefixToKernelMap(state, kernels)
	for prefix, kernelURN := range prefixMap {
		if strings.HasPrefix(urnText, prefix) {
			return kernelURN
		}
	}

	if primary := primaryKernel(kernels); primary != "" {
		return primary
	}
	return kernels[0]
}

func shardPrefixToKernelMap(state graph.GraphState, kernels []graph.URN) map[string]graph.URN {
	kernelSet := make(map[string]graph.URN, len(kernels))
	for _, k := range kernels {
		kernelSet[k.String()] = k
	}

	out := make(map[string]graph.URN)
	for shardURN, node := range state.Nodes {
		if node.TypeID != "shard_rule" {
			continue
		}
		prop, ok := node.Properties["urn_prefix"]
		if !ok {
			continue
		}
		prefix, ok := prop.Value.(string)
		if !ok || strings.TrimSpace(prefix) == "" {
			continue
		}
		
		for _, rel := range state.Relations {
			if rel.SrcURN == shardURN && rel.SrcPort == "routes-to" {
				if kernelURN, hit := kernelSet[rel.TgtURN.String()]; hit {
					out[prefix] = kernelURN
				}
			}
		}
	}
	return out
}

func primaryKernel(kernels []graph.URN) graph.URN {
	for _, k := range kernels {
		if strings.HasSuffix(k.String(), ".primary") {
			return k
		}
	}
	if len(kernels) > 0 {
		return kernels[0]
	}
	return ""
}

func kernelAlias(kernelURN graph.URN) string {
	s := kernelURN.String()
	idx := strings.LastIndexByte(s, '.')
	if idx < 0 || idx == len(s)-1 {
		return ""
	}
	return s[idx+1:]
}

func hasAliasSegment(urnText, alias string) bool {
	parts := strings.Split(urnText, ":")
	if len(parts) < 3 {
		return false
	}

	for i := 2; i < len(parts); i++ {
		seg := parts[i]
		if seg == alias {
			return true
		}
		if strings.HasSuffix(seg, "."+alias) {
			return true
		}
		if strings.HasPrefix(seg, alias+".") {
			return true
		}
		if strings.Contains(seg, "."+alias+".") {
			return true
		}
	}

	return false
}

func l2Distance(a, b HV) float64 {
	var sum float64
	for i := 0; i < Dimension; i++ {
		d := float64(a[i] - b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}

func hvToSlice(v HV) []float32 {
	out := make([]float32, Dimension)
	copy(out, v[:])
	return out
}

// FiberVectorForKernel returns metadata + vector for one kernel.
func FiberVectorForKernel(state graph.GraphState, kernelURN graph.URN, enc *Encoder) FiberVectorResponse {
	v := EncodeFiber(state, kernelURN, enc)
	nodes := NodesInKernel(state, kernelURN)
	return FiberVectorResponse{Kernel: kernelURN, NodeCount: len(nodes), Vector: hvToSlice(v)}
}

// FederationVector returns metadata + vector for the full federation.
func FederationVector(state graph.GraphState, enc *Encoder) FederationVectorResponse {
	fibers := FiberVectorsByKernel(state, enc)
	v := EncodeFederation(fibers)
	return FederationVectorResponse{KernelCount: len(fibers), Vector: hvToSlice(v)}
}
