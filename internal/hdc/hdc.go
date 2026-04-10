package hdc

import (
	"hash/fnv"
	"math"
	"math/rand"

	"moos/kernel/internal/graph"
)

const Dimension = 10000

// HV is a fixed-size hypervector used for HDC encoding.
type HV [Dimension]float32

// Codebook stores deterministic basis vectors keyed by URN.
type Codebook map[graph.URN]HV

func NewCodebook() Codebook {
	return make(Codebook)
}

// Encode returns the basis vector for a URN, lazily creating it when missing.
func (cb Codebook) Encode(urn graph.URN) HV {
	if v, ok := cb[urn]; ok {
		return v
	}

	seed := urnSeed(urn)
	rng := rand.New(rand.NewSource(seed))

	var v HV
	for i := 0; i < Dimension; i++ {
		if rng.Intn(2) == 0 {
			v[i] = -1
		} else {
			v[i] = 1
		}
	}
	cb[urn] = v
	return v
}

func urnSeed(urn graph.URN) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(urn))
	return int64(h.Sum64())
}

// Bind composes two vectors via elementwise multiplication.
func Bind(a, b HV) HV {
	var out HV
	for i := 0; i < Dimension; i++ {
		out[i] = a[i] * b[i]
	}
	return out
}

// Unbind recovers a vector from a bound pair when the key is known.
func Unbind(bound, key HV) HV {
	return Bind(bound, key)
}

// Bundle superposes vectors and returns an L2-normalized result.
func Bundle(vs ...HV) HV {
	var out HV
	if len(vs) == 0 {
		return out
	}

	for _, v := range vs {
		for i := 0; i < Dimension; i++ {
			out[i] += v[i]
		}
	}

	var normSq float64
	for i := 0; i < Dimension; i++ {
		normSq += float64(out[i] * out[i])
	}
	if normSq == 0 {
		return out
	}

	norm := float32(math.Sqrt(normSq))
	for i := 0; i < Dimension; i++ {
		out[i] /= norm
	}

	return out
}

// Permute rotates a vector by k positions.
func Permute(v HV, k int) HV {
	var out HV
	shift := k % Dimension
	if shift < 0 {
		shift += Dimension
	}
	if shift == 0 {
		return v
	}

	for i := 0; i < Dimension; i++ {
		out[(i+shift)%Dimension] = v[i]
	}
	return out
}

// Cosine computes cosine similarity in [-1, 1].
func Cosine(a, b HV) float32 {
	var dot float64
	var na float64
	var nb float64
	for i := 0; i < Dimension; i++ {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
