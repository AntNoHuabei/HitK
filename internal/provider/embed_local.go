package provider

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HashEmbedder is the offline embedding fallback.
//
// It is a hashing-trick bag-of-features model: tokens (ASCII words, CJK
// unigrams and bigrams) are hashed into a fixed-width vector, weighted by
// sublinear term frequency, then L2-normalised. Cosine similarity over these
// vectors approximates lexical overlap with synonym tolerance from the bigram
// collisions — enough to make hybrid retrieval useful with zero configuration
// and zero network calls.
//
// It is deliberately not a neural model. When the user configures an embedding
// endpoint HitK prefers [RemoteEmbedder] and keeps this as the backstop so a
// knowledge base remains searchable offline.
type HashEmbedder struct {
	dims int
}

// NewHashEmbedder builds the offline embedder. dims is clamped to a sane range.
func NewHashEmbedder(dims int) *HashEmbedder {
	if dims < 128 {
		dims = 512
	}
	if dims > 4096 {
		dims = 4096
	}
	return &HashEmbedder{dims: dims}
}

// ID implements [Embedder].
func (h *HashEmbedder) ID() string { return "local/hash-bow" }

// Dimensions implements [Embedder].
func (h *HashEmbedder) Dimensions() int { return h.dims }

// Embed implements [Embedder].
func (h *HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.vector(t)
	}
	return out, nil
}

// vector computes the sparse-to-dense hashed representation of one string.
func (h *HashEmbedder) vector(text string) []float32 {
	vec := make([]float32, h.dims)
	terms := Tokenize(text)
	if len(terms) == 0 {
		return vec
	}

	counts := map[string]int{}
	for _, t := range terms {
		counts[t]++
	}
	for term, n := range counts {
		// Sublinear tf keeps a repeated term from dominating the vector.
		w := float32(1 + math.Log(float64(n)))
		idx, sign := h.bucket(term)
		vec[idx] += sign * w
	}

	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

// bucket maps a term to a dimension and a sign, using a second hash for the
// sign so collisions cancel instead of always reinforcing.
func (h *HashEmbedder) bucket(term string) (int, float32) {
	hv := fnv.New32a()
	_, _ = hv.Write([]byte(term))
	sum := hv.Sum32()
	idx := int(sum % uint32(h.dims))

	sv := fnv.New32a()
	_, _ = sv.Write([]byte("\x00" + term))
	if sv.Sum32()&1 == 1 {
		return idx, 1
	}
	return idx, -1
}

// Tokenize splits text into indexable terms: lower-cased ASCII words plus CJK
// unigrams and bigrams. CJK has no whitespace, so bigrams are what give the
// hash vectors any notion of multi-character terms.
func Tokenize(text string) []string {
	var (
		terms []string
		word  strings.Builder
		cjk   []rune
	)
	flushWord := func() {
		if word.Len() > 0 {
			terms = append(terms, word.String())
			word.Reset()
		}
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		for _, r := range cjk {
			terms = append(terms, string(r))
		}
		for i := 0; i+1 < len(cjk); i++ {
			terms = append(terms, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}

	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			flushCJK()
			word.WriteRune(unicode.ToLower(r))
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return terms
}

// isCJK reports whether r belongs to a script written without word spacing.
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // Extension A
		return true
	case r >= 0xF900 && r <= 0xFAFF: // Compatibility Ideographs
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	}
	return false
}

// Cosine returns the cosine similarity of two equal-length vectors.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
