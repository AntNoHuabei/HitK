package kb

import (
	"math"
	"sort"
	"sync"

	"github.com/hitk/hitk/internal/provider"
)

// BM25 parameters. The defaults are the standard ones from the Robertson
// formulation; b = 0.75 means long chunks are penalised but not crushed.
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// indexedChunk is the per-chunk record kept resident in the index.
type indexedChunk struct {
	chunkID      string
	documentID   string
	collectionID string
	ordinal      int
	heading      string
	text         string
	length       int
}

// posting is one term occurrence inside a chunk.
type posting struct {
	idx int32
	tf  int32
}

// lexicalScore is one BM25 result before fusion.
type lexicalScore struct {
	idx   int32
	score float64
}

// Index is the in-memory retrieval structure: a BM25 inverted index over all
// chunks plus one embedding per chunk. It is rebuilt from the store on open and
// mutated incrementally as documents are added.
type Index struct {
	mu sync.RWMutex

	chunks   []indexedChunk
	postings map[string][]posting
	vectors  [][]float32
	totalLen int64
	// docFreq counts how many chunks contain each term.
	docFreq map[string]int32
	// pos maps chunk id to its slot, so an updated chunk replaces its old row.
	pos map[string]int32
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{
		postings: map[string][]posting{},
		docFreq:  map[string]int32{},
		pos:      map[string]int32{},
	}
}

// Len reports how many chunks are indexed.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.chunks)
}

// Add inserts or replaces a chunk and its vector.
func (ix *Index) Add(c Chunk, vec []float32) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	// Replacing an existing chunk would leave stale postings behind, and
	// removing entries from a postings list is far more code than the rare
	// update warrants; the store rebuilds the whole index on re-ingest instead.
	if _, exists := ix.pos[c.ID]; exists {
		return
	}

	slot := int32(len(ix.chunks))
	terms := provider.Tokenize(c.Text + " " + c.Heading)
	ix.chunks = append(ix.chunks, indexedChunk{
		chunkID:      c.ID,
		documentID:   c.DocumentID,
		collectionID: c.CollectionID,
		ordinal:      c.Ordinal,
		heading:      c.Heading,
		text:         c.Text,
		length:       len(terms),
	})
	ix.pos[c.ID] = slot
	ix.totalLen += int64(len(terms))

	if vec == nil {
		ix.vectors = append(ix.vectors, nil)
	} else {
		ix.vectors = append(ix.vectors, vec)
	}

	counts := map[string]int32{}
	for _, t := range terms {
		counts[t]++
	}
	for term, tf := range counts {
		ix.postings[term] = append(ix.postings[term], posting{idx: slot, tf: tf})
		ix.docFreq[term]++
	}
}

// Reset clears the index.
func (ix *Index) Reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.chunks = nil
	ix.vectors = nil
	ix.postings = map[string][]posting{}
	ix.docFreq = map[string]int32{}
	ix.pos = map[string]int32{}
	ix.totalLen = 0
}

// Lexical runs BM25 over the index. collectionFilter, when non-empty, keeps
// only chunks whose collection is in the set.
func (ix *Index) Lexical(query string, filter map[string]bool) []lexicalScore {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if len(ix.chunks) == 0 {
		return nil
	}
	terms := provider.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	n := float64(len(ix.chunks))
	avgdl := float64(ix.totalLen) / n
	if avgdl <= 0 {
		avgdl = 1
	}

	// Dedupe query terms so a repeated word does not double its weight.
	seen := map[string]bool{}
	scores := map[int32]float64{}

	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true

		plist := ix.postings[term]
		if len(plist) == 0 {
			continue
		}
		df := float64(ix.docFreq[term])
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))

		for _, p := range plist {
			chunk := &ix.chunks[p.idx]
			if filter != nil && !filter[chunk.collectionID] {
				continue
			}
			dl := float64(chunk.length)
			if dl == 0 {
				dl = 1
			}
			tf := float64(p.tf)
			denom := tf + bm25K1*(1-bm25B+bm25B*dl/avgdl)
			if denom == 0 {
				continue
			}
			scores[p.idx] += idf * (tf * (bm25K1 + 1)) / denom
		}
	}

	out := make([]lexicalScore, 0, len(scores))
	for idx, s := range scores {
		out = append(out, lexicalScore{idx: idx, score: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// Vector runs cosine similarity over the stored embeddings.
func (ix *Index) Vector(qvec []float32, filter map[string]bool) []lexicalScore {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if len(qvec) == 0 {
		return nil
	}
	out := make([]lexicalScore, 0, 64)
	for i, v := range ix.vectors {
		if v == nil {
			continue
		}
		if filter != nil && !filter[ix.chunks[i].collectionID] {
			continue
		}
		s := provider.Cosine(qvec, v)
		if s <= 0 {
			continue
		}
		out = append(out, lexicalScore{idx: int32(i), score: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// chunkAt returns a copy of an indexed chunk.
func (ix *Index) chunkAt(idx int32) (indexedChunk, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if idx < 0 || int(idx) >= len(ix.chunks) {
		return indexedChunk{}, false
	}
	return ix.chunks[idx], true
}

// snapshot returns every indexed chunk, used when persisting vectors.
func (ix *Index) snapshot() []indexedChunk {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]indexedChunk, len(ix.chunks))
	copy(out, ix.chunks)
	return out
}
