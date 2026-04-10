package hdc

import (
	"math"
	"sort"

	"moos/kernel/internal/graph"
)

const eps = 1e-9

type SimilarityEntry struct {
	Src graph.URN `json:"src"`
	Tgt graph.URN `json:"tgt"`
	Cos float64   `json:"cos"`
}

type FiedlerEntry struct {
	URN   graph.URN `json:"urn"`
	Sign  string    `json:"sign"`
	Value float64   `json:"value"`
}

type TypeDrift struct {
	URN        graph.URN `json:"urn"`
	DriftScore float64   `json:"drift_score"`
}

type TypeCoherenceEntry struct {
	TypeID       graph.TypeID `json:"type_id"`
	CentroidNorm float64      `json:"centroid_norm"`
	Nodes        []TypeDrift  `json:"nodes"`
}

type nodeEmbedding struct {
	urn    graph.URN
	typeID graph.TypeID
	vector HV
}

// SimilarityMatrix computes pairwise cosine similarity for all nodes in a state.
func SimilarityMatrix(state graph.GraphState, enc *Encoder) ([]graph.URN, []graph.TypeID, [][]float64, []SimilarityEntry) {
	embeddings := embeddingsFromState(state, enc)
	n := len(embeddings)

	urns := make([]graph.URN, n)
	types := make([]graph.TypeID, n)
	for i, e := range embeddings {
		urns[i] = e.urn
		types[i] = e.typeID
	}

	matrix := make([][]float64, n)
	entries := make([]SimilarityEntry, 0, n*n)
	for i := 0; i < n; i++ {
		matrix[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			cos := float64(Cosine(embeddings[i].vector, embeddings[j].vector))
			matrix[i][j] = cos
			entries = append(entries, SimilarityEntry{Src: urns[i], Tgt: urns[j], Cos: cos})
		}
	}

	return urns, types, matrix, entries
}

// Laplacian constructs an unnormalized graph Laplacian from a similarity matrix.
// Negative similarities are clamped to 0 to keep the adjacency non-negative.
func Laplacian(similarity [][]float64) [][]float64 {
	n := len(similarity)
	lap := make([][]float64, n)
	for i := 0; i < n; i++ {
		lap[i] = make([]float64, n)
		var degree float64
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			w := similarity[i][j]
			if w < 0 {
				w = 0
			}
			degree += w
			lap[i][j] = -w
		}
		lap[i][i] = degree
	}
	return lap
}

// EigenDecompositionSymmetric computes eigenvalues and eigenvectors using Jacobi rotations.
// Returned eigenvalues are sorted ascending, and eigenvectors are returned by column index.
func EigenDecompositionSymmetric(matrix [][]float64) ([]float64, [][]float64) {
	n := len(matrix)
	if n == 0 {
		return nil, nil
	}

	a := copyMatrix(matrix)
	v := identityMatrix(n)

	maxIter := 50 * n * n
	for iter := 0; iter < maxIter; iter++ {
		p, q, maxOffDiag := maxOffDiagonal(a)
		if maxOffDiag < 1e-10 {
			break
		}

		phi := 0.5 * math.Atan2(2*a[p][q], a[q][q]-a[p][p])
		c := math.Cos(phi)
		s := math.Sin(phi)

		for i := 0; i < n; i++ {
			if i == p || i == q {
				continue
			}
			aip := a[i][p]
			aiq := a[i][q]
			a[i][p] = c*aip - s*aiq
			a[p][i] = a[i][p]
			a[i][q] = s*aip + c*aiq
			a[q][i] = a[i][q]
		}

		app := a[p][p]
		aqq := a[q][q]
		apq := a[p][q]
		a[p][p] = c*c*app - 2*s*c*apq + s*s*aqq
		a[q][q] = s*s*app + 2*s*c*apq + c*c*aqq
		a[p][q] = 0
		a[q][p] = 0

		for i := 0; i < n; i++ {
			vip := v[i][p]
			viq := v[i][q]
			v[i][p] = c*vip - s*viq
			v[i][q] = s*vip + c*viq
		}
	}

	eigenvalues := make([]float64, n)
	for i := 0; i < n; i++ {
		eigenvalues[i] = a[i][i]
	}

	return sortEigenpairs(eigenvalues, v)
}

// FiedlerPartition returns a 2-way partition from the second-smallest eigenvector.
func FiedlerPartition(urns []graph.URN, eigenvalues []float64, eigenvectors [][]float64) []FiedlerEntry {
	n := len(urns)
	if n == 0 || len(eigenvalues) < 2 || len(eigenvectors) != n {
		return nil
	}

	fiedlerIndex := 1
	out := make([]FiedlerEntry, 0, n)
	for i := 0; i < n; i++ {
		if fiedlerIndex >= len(eigenvectors[i]) {
			continue
		}
		v := eigenvectors[i][fiedlerIndex]
		sign := "zero"
		if v > eps {
			sign = "positive"
		} else if v < -eps {
			sign = "negative"
		}
		out = append(out, FiedlerEntry{URN: urns[i], Sign: sign, Value: v})
	}
	return out
}

// CheegerConstant estimates h(G) using the Fiedler sign cut conductance.
func CheegerConstant(similarity [][]float64, eigenvalues []float64, eigenvectors [][]float64) float64 {
	n := len(similarity)
	if n == 0 || len(eigenvalues) < 2 || len(eigenvectors) != n {
		return 0
	}

	inS := make([]bool, n)
	for i := 0; i < n; i++ {
		if len(eigenvectors[i]) > 1 {
			inS[i] = eigenvectors[i][1] >= 0
		}
	}

	degrees := make([]float64, n)
	var cut float64
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			w := similarity[i][j]
			if w < 0 {
				w = 0
			}
			degrees[i] += w
			if inS[i] != inS[j] {
				cut += w
			}
		}
	}
	cut /= 2

	var volS float64
	var volT float64
	for i := 0; i < n; i++ {
		if inS[i] {
			volS += degrees[i]
		} else {
			volT += degrees[i]
		}
	}
	denom := math.Min(volS, volT)
	if denom <= eps {
		return 0
	}
	return cut / denom
}

// TypeCoherence returns centroid drift information for each declared node type.
func TypeCoherence(state graph.GraphState, enc *Encoder) []TypeCoherenceEntry {
	embeddings := embeddingsFromState(state, enc)
	if len(embeddings) == 0 {
		return nil
	}

	groups := make(map[graph.TypeID][]nodeEmbedding)
	for _, e := range embeddings {
		groups[e.typeID] = append(groups[e.typeID], e)
	}

	out := make([]TypeCoherenceEntry, 0, len(groups))
	for typeID, group := range groups {
		var raw HV
		for _, e := range group {
			for i := 0; i < Dimension; i++ {
				raw[i] += e.vector[i]
			}
		}

		centroidNorm := l2Norm(raw)
		centroid := raw
		if centroidNorm > eps {
			inv := float32(1.0 / centroidNorm)
			for i := 0; i < Dimension; i++ {
				centroid[i] *= inv
			}
		}

		nodes := make([]TypeDrift, 0, len(group))
		for _, e := range group {
			drift := 1.0
			if centroidNorm > eps {
				drift = 1 - float64(Cosine(e.vector, centroid))
			}
			nodes = append(nodes, TypeDrift{URN: e.urn, DriftScore: drift})
		}
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].DriftScore == nodes[j].DriftScore {
				return nodes[i].URN < nodes[j].URN
			}
			return nodes[i].DriftScore > nodes[j].DriftScore
		})

		out = append(out, TypeCoherenceEntry{
			TypeID:       typeID,
			CentroidNorm: centroidNorm,
			Nodes:        nodes,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].TypeID < out[j].TypeID })
	return out
}

// ClusterCountByEpsilon approximates connected spectral components by counting near-zero eigenvalues.
func ClusterCountByEpsilon(eigenvalues []float64, threshold float64) int {
	if threshold <= 0 {
		threshold = 1e-6
	}
	count := 0
	for _, v := range eigenvalues {
		if math.Abs(v) <= threshold {
			count++
		}
	}
	return count
}

func embeddingsFromState(state graph.GraphState, enc *Encoder) []nodeEmbedding {
	if enc == nil {
		enc = NewEncoder()
	}

	urns := make([]graph.URN, 0, len(state.Nodes))
	for urn := range state.Nodes {
		urns = append(urns, urn)
	}
	sort.Slice(urns, func(i, j int) bool { return urns[i] < urns[j] })

	out := make([]nodeEmbedding, 0, len(urns))
	for _, urn := range urns {
		node := state.Nodes[urn]
		out = append(out, nodeEmbedding{
			urn:    urn,
			typeID: node.TypeID,
			vector: enc.EncodeNode(state, urn),
		})
	}
	return out
}

func l2Norm(v HV) float64 {
	var sum float64
	for i := 0; i < Dimension; i++ {
		x := float64(v[i])
		sum += x * x
	}
	return math.Sqrt(sum)
}

func copyMatrix(in [][]float64) [][]float64 {
	n := len(in)
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, len(in[i]))
		copy(out[i], in[i])
	}
	return out
}

func identityMatrix(n int) [][]float64 {
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, n)
		out[i][i] = 1
	}
	return out
}

func maxOffDiagonal(a [][]float64) (int, int, float64) {
	n := len(a)
	p, q := 0, 0
	maxAbs := 0.0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			v := math.Abs(a[i][j])
			if v > maxAbs {
				maxAbs = v
				p = i
				q = j
			}
		}
	}
	return p, q, maxAbs
}

func sortEigenpairs(values []float64, vectors [][]float64) ([]float64, [][]float64) {
	n := len(values)
	order := make([]int, n)
	for i := 0; i < n; i++ {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return values[order[i]] < values[order[j]]
	})

	sortedValues := make([]float64, n)
	sortedVectors := make([][]float64, len(vectors))
	for i := range vectors {
		sortedVectors[i] = make([]float64, n)
	}

	for k, idx := range order {
		sortedValues[k] = values[idx]
		for row := range vectors {
			sortedVectors[row][k] = vectors[row][idx]
		}
	}

	return sortedValues, sortedVectors
}
