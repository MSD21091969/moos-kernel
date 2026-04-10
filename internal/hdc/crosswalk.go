package hdc

import (
	"math"
	"sort"
	"strings"

	"moos/kernel/internal/graph"
)

const crosswalkDim = 64

// CrosswalkResult returns the orthogonal map and residual from one scheme to another.
type CrosswalkResult struct {
	From          string      `json:"from"`
	To            string      `json:"to"`
	Dimension     int         `json:"dimension"`
	ResidualError float64     `json:"residual_error"`
	Matrix        [][]float64 `json:"rotation_matrix"`
}

// CompositionCheckEntry compares composed vs direct crosswalk transforms.
type CompositionCheckEntry struct {
	Chain    string      `json:"chain"`
	Error    float64     `json:"error"`
	Composed [][]float64 `json:"composed_R"`
	Direct   [][]float64 `json:"direct_R"`
}

// CrosswalkSuggestion proposes a missing relation based on vector similarity.
type CrosswalkSuggestion struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Cosine float64 `json:"cosine"`
}

// ClassificationPoint is a scheme embedding projected to 3D PCA space.
type ClassificationPoint struct {
	Scheme  string  `json:"scheme"`
	Members int     `json:"members"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Z       float64 `json:"z"`
}

// ClassificationSpaceResponse returns scheme points and PCA explained variance.
type ClassificationSpaceResponse struct {
	Points            []ClassificationPoint `json:"points"`
	ExplainedVariance []float64             `json:"explained_variance"`
}

// ComputeCrosswalk computes an orthogonal transform that aligns source to target scheme encodings.
func ComputeCrosswalk(state graph.GraphState, from, to string, enc *Encoder) (CrosswalkResult, bool) {
	schemes := SchemeVectors(state, enc)
	fromVec, okFrom := schemes[strings.ToLower(strings.TrimSpace(from))]
	toVec, okTo := schemes[strings.ToLower(strings.TrimSpace(to))]
	if !okFrom || !okTo {
		return CrosswalkResult{}, false
	}

	a := reduceHV(fromVec.Vector, crosswalkDim)
	b := reduceHV(toVec.Vector, crosswalkDim)
	rotation := OrthogonalRotation(a, b)
	residual := normalizedResidual(rotation, a, b)

	return CrosswalkResult{
		From:          fromVec.Name,
		To:            toVec.Name,
		Dimension:     crosswalkDim,
		ResidualError: residual,
		Matrix:        rotation,
	}, true
}

// CrosswalkCompositionChecks compares composed and direct transforms for A->B->C chains.
func CrosswalkCompositionChecks(state graph.GraphState, enc *Encoder) []CompositionCheckEntry {
	schemes := SchemeVectors(state, enc)
	names := sortedSchemeNames(schemes)
	if len(names) < 3 {
		return nil
	}

	out := make([]CompositionCheckEntry, 0)
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names); j++ {
			if i == j {
				continue
			}
			for k := 0; k < len(names); k++ {
				if k == i || k == j {
					continue
				}

				a := reduceHV(schemes[names[i]].Vector, crosswalkDim)
				b := reduceHV(schemes[names[j]].Vector, crosswalkDim)
				c := reduceHV(schemes[names[k]].Vector, crosswalkDim)

				rab := OrthogonalRotation(a, b)
				rbc := OrthogonalRotation(b, c)
				rac := OrthogonalRotation(a, c)
				composed := matMul(rbc, rab)
				err := normalizedMatrixError(composed, rac)

				out = append(out, CompositionCheckEntry{
					Chain:    schemes[names[i]].Name + "->" + schemes[names[j]].Name + "->" + schemes[names[k]].Name,
					Error:    err,
					Composed: composed,
					Direct:   rac,
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Error == out[j].Error {
			return out[i].Chain < out[j].Chain
		}
		return out[i].Error < out[j].Error
	})
	return out
}

// CrosswalkSuggestions returns high-similarity scheme pairs that do not already have explicit crosswalk links.
func CrosswalkSuggestions(state graph.GraphState, enc *Encoder, threshold float64) []CrosswalkSuggestion {
	if threshold <= 0 {
		threshold = 0.7
	}

	schemes := SchemeVectors(state, enc)
	names := sortedSchemeNames(schemes)
	if len(names) < 2 {
		return nil
	}

	explicit := explicitCrosswalkPairs(state)
	out := make([]CrosswalkSuggestion, 0)
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a := schemes[names[i]].Vector
			b := schemes[names[j]].Vector
			cos := float64(Cosine(a, b))
			if cos < threshold {
				continue
			}

			pair := names[i] + "|" + names[j]
			if _, ok := explicit[pair]; ok {
				continue
			}

			out = append(out, CrosswalkSuggestion{
				From:   schemes[names[i]].Name,
				To:     schemes[names[j]].Name,
				Cosine: cos,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Cosine == out[j].Cosine {
			if out[i].From == out[j].From {
				return out[i].To < out[j].To
			}
			return out[i].From < out[j].From
		}
		return out[i].Cosine > out[j].Cosine
	})
	return out
}

// ClassificationSpace projects schemes into 3D PCA space.
func ClassificationSpace(state graph.GraphState, enc *Encoder) ClassificationSpaceResponse {
	schemes := SchemeVectors(state, enc)
	names := sortedSchemeNames(schemes)
	if len(names) == 0 {
		return ClassificationSpaceResponse{}
	}

	dim := crosswalkDim
	matrix := make([][]float64, len(names))
	for i, name := range names {
		matrix[i] = reduceHV(schemes[name].Vector, dim)
	}

	centered, means := centerRows(matrix)
	_ = means
	cov := covariance(centered)
	eigvals, eigvecs := EigenDecompositionSymmetric(cov)

	componentIdx := topEigenIndices(eigvals, 3)
	points := make([]ClassificationPoint, 0, len(names))
	for i, name := range names {
		coords := projectToComponents(centered[i], eigvecs, componentIdx)
		for len(coords) < 3 {
			coords = append(coords, 0)
		}
		points = append(points, ClassificationPoint{
			Scheme:  schemes[name].Name,
			Members: len(schemes[name].Members),
			X:       coords[0],
			Y:       coords[1],
			Z:       coords[2],
		})
	}

	explained := explainedVariance(eigvals, componentIdx)
	return ClassificationSpaceResponse{Points: points, ExplainedVariance: explained}
}

// SchemeVector is one encoded classification scheme.
type SchemeVector struct {
	Name    string
	Members []graph.URN
	Vector  HV
}

// SchemeVectors extracts and encodes classification schemes found in graph state.
func SchemeVectors(state graph.GraphState, enc *Encoder) map[string]SchemeVector {
	if enc == nil {
		enc = NewEncoder()
	}

	groups := make(map[string][]graph.URN)
	for urn, node := range state.Nodes {
		scheme := schemeNameForNode(urn, node)
		if scheme == "" {
			continue
		}
		groups[scheme] = append(groups[scheme], urn)
	}

	out := make(map[string]SchemeVector, len(groups))
	for scheme, members := range groups {
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		vectors := make([]HV, 0, len(members))
		for _, urn := range members {
			vectors = append(vectors, enc.EncodeNode(state, urn))
		}
		out[scheme] = SchemeVector{
			Name:    scheme,
			Members: members,
			Vector:  Bundle(vectors...),
		}
	}

	return out
}

func schemeNameForNode(urn graph.URN, node graph.Node) string {
	if p, ok := node.Properties["scheme"]; ok {
		if s, ok := p.Value.(string); ok {
			s = strings.TrimSpace(strings.ToLower(s))
			if s != "" {
				return s
			}
		}
	}

	u := strings.ToLower(urn.String())
	known := []string{"arxiv", "lcc", "ifrs", "iso", "dewey"}
	for _, k := range known {
		if strings.Contains(u, k) {
			return k
		}
	}
	return ""
}

func explicitCrosswalkPairs(state graph.GraphState) map[string]struct{} {
	pairs := make(map[string]struct{})
	for _, rel := range state.Relations {
		if !looksLikeCrosswalkRelation(rel) {
			continue
		}
		a := inferSchemeFromURN(rel.SrcURN)
		b := inferSchemeFromURN(rel.TgtURN)
		if a == "" || b == "" || a == b {
			continue
		}
		pair := orderedPair(a, b)
		pairs[pair] = struct{}{}
	}
	return pairs
}

func looksLikeCrosswalkRelation(rel graph.Relation) bool {
	if rel.RewriteCategory == graph.WF15 {
		return true
	}
	text := strings.ToLower(rel.URN.String() + ":" + rel.SrcPort + ":" + rel.TgtPort)
	return strings.Contains(text, "crosswalk") || strings.Contains(text, "map") || strings.Contains(text, "align")
}

func inferSchemeFromURN(urn graph.URN) string {
	u := strings.ToLower(urn.String())
	known := []string{"arxiv", "lcc", "ifrs", "iso", "dewey"}
	for _, k := range known {
		if strings.Contains(u, k) {
			return k
		}
	}
	return ""
}

func orderedPair(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

func sortedSchemeNames(s map[string]SchemeVector) []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OrthogonalRotation computes a dxd orthogonal matrix that aligns vector a to b.
func OrthogonalRotation(a, b []float64) [][]float64 {
	n := len(a)
	if n == 0 || len(b) != n {
		return identity(n)
	}

	u := normalize(a)
	vTarget := normalize(b)
	if l2(u) == 0 || l2(vTarget) == 0 {
		return identity(n)
	}

	c := clamp(dot(u, vTarget), -1, 1)
	if math.Abs(1-c) < 1e-12 {
		return identity(n)
	}

	vRaw := make([]float64, n)
	for i := 0; i < n; i++ {
		vRaw[i] = vTarget[i] - c*u[i]
	}
	if l2(vRaw) < 1e-12 {
		vRaw = orthogonalBasisVector(u)
	}
	v := normalize(vRaw)
	s := math.Sqrt(math.Max(0, 1-c*c))

	r := identity(n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r[i][j] += (c-1)*(u[i]*u[j]+v[i]*v[j]) + s*(v[i]*u[j]-u[i]*v[j])
		}
	}
	return r
}

func orthogonalBasisVector(u []float64) []float64 {
	n := len(u)
	if n == 0 {
		return nil
	}
	k := 0
	minAbs := math.Abs(u[0])
	for i := 1; i < n; i++ {
		if math.Abs(u[i]) < minAbs {
			minAbs = math.Abs(u[i])
			k = i
		}
	}

	v := make([]float64, n)
	v[k] = 1
	proj := dot(v, u)
	for i := 0; i < n; i++ {
		v[i] -= proj * u[i]
	}
	return v
}

func normalizedResidual(r [][]float64, a, b []float64) float64 {
	ra := matVecMul(r, a)
	delta := make([]float64, len(a))
	for i := range a {
		delta[i] = ra[i] - b[i]
	}
	den := l2(b)
	if den == 0 {
		den = 1
	}
	return l2(delta) / den
}

func normalizedMatrixError(a, b [][]float64) float64 {
	diff := frobeniusNormDiff(a, b)
	den := frobeniusNorm(b)
	if den == 0 {
		den = 1
	}
	return diff / den
}

func reduceHV(v HV, dim int) []float64 {
	if dim <= 0 || dim > Dimension {
		dim = Dimension
	}
	out := make([]float64, dim)
	for i := 0; i < dim; i++ {
		out[i] = float64(v[i])
	}
	return out
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func l2(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x * x
	}
	return math.Sqrt(s)
}

func normalize(v []float64) []float64 {
	n := l2(v)
	if n == 0 {
		out := make([]float64, len(v))
		copy(out, v)
		return out
	}
	out := make([]float64, len(v))
	for i := range v {
		out[i] = v[i] / n
	}
	return out
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func identity(n int) [][]float64 {
	if n <= 0 {
		return nil
	}
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, n)
		out[i][i] = 1
	}
	return out
}

func matVecMul(m [][]float64, v []float64) []float64 {
	out := make([]float64, len(m))
	for i := range m {
		s := 0.0
		for j := range m[i] {
			s += m[i][j] * v[j]
		}
		out[i] = s
	}
	return out
}

func matMul(a, b [][]float64) [][]float64 {
	n := len(a)
	if n == 0 {
		return nil
	}
	m := len(b[0])
	p := len(b)
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, m)
		for j := 0; j < m; j++ {
			s := 0.0
			for k := 0; k < p; k++ {
				s += a[i][k] * b[k][j]
			}
			out[i][j] = s
		}
	}
	return out
}

func frobeniusNorm(m [][]float64) float64 {
	s := 0.0
	for i := range m {
		for j := range m[i] {
			s += m[i][j] * m[i][j]
		}
	}
	return math.Sqrt(s)
}

func frobeniusNormDiff(a, b [][]float64) float64 {
	s := 0.0
	for i := range a {
		for j := range a[i] {
			d := a[i][j] - b[i][j]
			s += d * d
		}
	}
	return math.Sqrt(s)
}

func centerRows(in [][]float64) ([][]float64, []float64) {
	if len(in) == 0 {
		return nil, nil
	}
	d := len(in[0])
	means := make([]float64, d)
	for _, row := range in {
		for j := 0; j < d; j++ {
			means[j] += row[j]
		}
	}
	for j := 0; j < d; j++ {
		means[j] /= float64(len(in))
	}

	out := make([][]float64, len(in))
	for i, row := range in {
		out[i] = make([]float64, d)
		for j := 0; j < d; j++ {
			out[i][j] = row[j] - means[j]
		}
	}
	return out, means
}

func covariance(centered [][]float64) [][]float64 {
	if len(centered) == 0 {
		return nil
	}
	n := len(centered)
	d := len(centered[0])
	cov := make([][]float64, d)
	for i := 0; i < d; i++ {
		cov[i] = make([]float64, d)
	}
	den := float64(n - 1)
	if den <= 0 {
		den = 1
	}
	for _, row := range centered {
		for i := 0; i < d; i++ {
			for j := 0; j < d; j++ {
				cov[i][j] += row[i] * row[j]
			}
		}
	}
	for i := 0; i < d; i++ {
		for j := 0; j < d; j++ {
			cov[i][j] /= den
		}
	}
	return cov
}

func topEigenIndices(eigvals []float64, k int) []int {
	if k <= 0 || len(eigvals) == 0 {
		return nil
	}
	if k > len(eigvals) {
		k = len(eigvals)
	}
	idx := make([]int, 0, k)
	for i := len(eigvals) - 1; i >= 0 && len(idx) < k; i-- {
		idx = append(idx, i)
	}
	return idx
}

func projectToComponents(row []float64, eigvecs [][]float64, idx []int) []float64 {
	out := make([]float64, 0, len(idx))
	for _, col := range idx {
		s := 0.0
		for r := 0; r < len(row) && r < len(eigvecs); r++ {
			if col < len(eigvecs[r]) {
				s += row[r] * eigvecs[r][col]
			}
		}
		out = append(out, s)
	}
	return out
}

func explainedVariance(eigvals []float64, idx []int) []float64 {
	if len(eigvals) == 0 {
		return nil
	}
	total := 0.0
	for _, v := range eigvals {
		if v > 0 {
			total += v
		}
	}
	if total == 0 {
		return make([]float64, len(idx))
	}
	out := make([]float64, len(idx))
	for i, j := range idx {
		v := eigvals[j]
		if v < 0 {
			v = 0
		}
		out[i] = v / total
	}
	return out
}
