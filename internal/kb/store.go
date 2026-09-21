package kb

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"
)

// Bucket names. Chunk bytes dominate the file, so they live alone.
var (
	bktCollections = []byte("collections")
	bktDocuments   = []byte("documents")
	bktChunks      = []byte("chunks")
	bktVectors     = []byte("vectors")
	bktDocChunks   = []byte("docchunks")
	bktMeta        = []byte("meta")
)

// Store is the knowledge-base persistence and retrieval engine.
type Store struct {
	db      *bolt.DB
	chunker *Chunker
	index   *Index

	mu       sync.RWMutex
	embedder Embedder
	// embedding, when true, records that a remote embedder is authoritative and
	// vectors must be persisted; the local hash embedder is cheap enough to
	// recompute and is not written to disk.
	embedRemote bool
}

// Embedder is the subset of provider.Embedder the store needs. It is declared
// here so the kb package does not depend on concrete provider clients.
type Embedder interface {
	ID() string
	Dimensions() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Open opens or creates a knowledge base at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open knowledge base: %w", err)
	}
	s := &Store{db: db, chunker: NewChunker(DefaultChunkRunes, DefaultOverlap), index: NewIndex()}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bktCollections, bktDocuments, bktChunks, bktVectors, bktDocChunks, bktMeta} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := s.rebuildIndex(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close flushes and closes the store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SetEmbedder installs the embedding model used for new documents. Existing
// vectors are left alone: mixing models inside one index would make cosine
// similarity meaningless, so the caller is expected to re-index a collection
// after changing models.
func (s *Store) SetEmbedder(e Embedder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e == nil {
		s.embedder = nil
		s.embedRemote = false
		return
	}
	changed := s.embedder == nil || s.embedder.ID() != e.ID()
	s.embedder = e
	s.embedRemote = !strings.HasPrefix(e.ID(), "local/")
	if changed {
		// Vectors for the new model must be recomputed; the local fallback
		// produces them on the fly so nothing needs to be persisted for it.
		s.index.Reset()
		_ = s.rebuildIndex()
	}
}

// EmbedderID reports the active embedding model.
func (s *Store) EmbedderID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.embedder == nil {
		return "local/hash-bow"
	}
	return s.embedder.ID()
}

// ---------------------------------------------------------------------------
// Collections
// ---------------------------------------------------------------------------

// Collections lists every collection with refreshed counters.
func (s *Store) Collections() ([]Collection, error) {
	var out []Collection
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktCollections).ForEach(func(_, v []byte) error {
			var c Collection
			if err := json.Unmarshal(v, &c); err != nil {
				return nil // a corrupt row should not hide the healthy ones
			}
			out = append(out, c)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})
	for i := range out {
		if err := s.fillCounts(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Collection fetches one collection by id.
func (s *Store) Collection(id string) (Collection, error) {
	var c Collection
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktCollections).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("collection %s not found", id)
		}
		return json.Unmarshal(raw, &c)
	})
	if err != nil {
		return c, err
	}
	err = s.fillCounts(&c)
	return c, err
}

// CreateCollection adds a collection.
func (s *Store) CreateCollection(name, description string) (Collection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Collection{}, errors.New("知识库名称不能为空")
	}
	now := nowMS()
	c := Collection{
		ID:          "col_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
		Name:        name,
		Description: strings.TrimSpace(description),
		Icon:        "database",
		CreatedAt:   now,
		UpdatedAt:   now,
		EmbedderID:  s.EmbedderID(),
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		// Names are the primary handle users type, so collisions must be
		// rejected rather than silently creating a look-alike.
		b := tx.Bucket(bktCollections)
		if err := b.ForEach(func(_, v []byte) error {
			var other Collection
			if json.Unmarshal(v, &other) == nil && strings.EqualFold(other.Name, name) {
				return fmt.Errorf("已存在同名知识库 %q", name)
			}
			return nil
		}); err != nil {
			return err
		}
		raw, _ := json.Marshal(c)
		return b.Put([]byte(c.ID), raw)
	})
	return c, err
}

// UpdateCollection patches mutable fields.
func (s *Store) UpdateCollection(id, name, description string) (Collection, error) {
	var c Collection
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bktCollections)
		raw := b.Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("知识库不存在")
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		if n := strings.TrimSpace(name); n != "" {
			c.Name = n
		}
		c.Description = strings.TrimSpace(description)
		c.UpdatedAt = nowMS()
		out, _ := json.Marshal(c)
		return b.Put([]byte(id), out)
	})
	return c, err
}

// DeleteCollection removes a collection with all of its documents.
func (s *Store) DeleteCollection(id string) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		docs := tx.Bucket(bktDocuments)
		var docIDs []string
		if err := docs.ForEach(func(k, v []byte) error {
			var d Document
			if json.Unmarshal(v, &d) == nil && d.CollectionID == id {
				docIDs = append(docIDs, string(k))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, did := range docIDs {
			if err := removeDocumentTx(tx, did); err != nil {
				return err
			}
		}
		return tx.Bucket(bktCollections).Delete([]byte(id))
	})
	if err != nil {
		return err
	}
	return s.rebuildIndex()
}

// fillCounts recomputes a collection's derived counters.
func (s *Store) fillCounts(c *Collection) error {
	return s.db.View(func(tx *bolt.Tx) error {
		c.DocCount, c.ChunkCount, c.SizeBytes = 0, 0, 0
		return tx.Bucket(bktDocuments).ForEach(func(_, v []byte) error {
			var d Document
			if json.Unmarshal(v, &d) != nil {
				return nil
			}
			if d.CollectionID != c.ID {
				return nil
			}
			c.DocCount++
			c.ChunkCount += d.ChunkCount
			c.SizeBytes += d.SizeBytes
			return nil
		})
	})
}

// ---------------------------------------------------------------------------
// Documents
// ---------------------------------------------------------------------------

// Documents lists the documents of a collection, newest first.
func (s *Store) Documents(collectionID string) ([]Document, error) {
	var out []Document
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktDocuments).ForEach(func(_, v []byte) error {
			var d Document
			if json.Unmarshal(v, &d) != nil {
				return nil
			}
			if collectionID == "" || d.CollectionID == collectionID {
				out = append(out, d)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Document fetches one document.
func (s *Store) Document(id string) (Document, error) {
	var d Document
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktDocuments).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("文档不存在")
		}
		return json.Unmarshal(raw, &d)
	})
	return d, err
}

// Chunks returns a document's chunks in order.
func (s *Store) Chunks(documentID string) ([]Chunk, error) {
	var out []Chunk
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktDocChunks).Get([]byte(documentID))
		if raw == nil {
			return nil
		}
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			return err
		}
		chunks := tx.Bucket(bktChunks)
		for _, id := range ids {
			cb := chunks.Get([]byte(id))
			if cb == nil {
				continue
			}
			var c Chunk
			if json.Unmarshal(cb, &c) == nil {
				out = append(out, c)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, err
}

// Chunk fetches one chunk by id.
func (s *Store) Chunk(id string) (Chunk, error) {
	var c Chunk
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktChunks).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("分块不存在")
		}
		return json.Unmarshal(raw, &c)
	})
	return c, err
}

// DeleteDocument removes a document and re-indexes.
func (s *Store) DeleteDocument(id string) error {
	if err := s.db.Update(func(tx *bolt.Tx) error { return removeDocumentTx(tx, id) }); err != nil {
		return err
	}
	return s.rebuildIndex()
}

// removeDocumentTx deletes a document's rows inside an existing transaction.
func removeDocumentTx(tx *bolt.Tx, docID string) error {
	chunks := tx.Bucket(bktChunks)
	vectors := tx.Bucket(bktVectors)
	docChunks := tx.Bucket(bktDocChunks)

	if raw := docChunks.Get([]byte(docID)); raw != nil {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil {
			for _, id := range ids {
				if err := chunks.Delete([]byte(id)); err != nil {
					return err
				}
				if err := vectors.Delete([]byte(id)); err != nil {
					return err
				}
			}
		}
		docChunks.Delete([]byte(docID))
	}
	return tx.Bucket(bktDocuments).Delete([]byte(docID))
}

// ---------------------------------------------------------------------------
// Ingestion
// ---------------------------------------------------------------------------

// IngestOptions tunes how a document is added.
type IngestOptions struct {
	// Title overrides the filename-derived title.
	Title string
	// Replace, when set, deletes a previously ingested document with the same
	// source before adding this one.
	Replace bool
	// Tags are free-form labels shown in the UI.
	Tags []string
}

// AddParsed indexes an already-parsed document.
func (s *Store) AddParsed(ctx context.Context, collectionID string, parsed *ParsedDocument, source, kind string, opts IngestOptions) (Document, error) {
	if parsed == nil {
		return Document{}, errors.New("空文档")
	}
	if _, err := s.Collection(collectionID); err != nil {
		return Document{}, err
	}
	if parsed.SkippedReason != "" {
		return Document{}, errors.New(parsed.SkippedReason)
	}
	text := strings.TrimSpace(parsed.Text)
	if text == "" {
		return Document{}, errors.New("文档内容为空")
	}

	hash := contentHash(text)
	if opts.Replace {
		if prev, ok := s.FindBySource(collectionID, source); ok {
			if prev.ContentHash == hash {
				// Unchanged content: re-indexing would only churn the index.
				return prev, nil
			}
			if err := s.DeleteDocument(prev.ID); err != nil {
				return Document{}, err
			}
		}
	}

	title := opts.Title
	if title == "" {
		title = parsed.Title
	}
	if title == "" {
		title = "未命名文档"
	}

	docID := "doc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	pieces := s.chunker.Split(text, title)
	if len(pieces) == 0 {
		return Document{}, errors.New("文档无法切分")
	}

	now := nowMS()
	doc := Document{
		ID:           docID,
		CollectionID: collectionID,
		Title:        title,
		Source:       source,
		Kind:         kind,
		Mime:         parsed.Mime,
		SizeBytes:    int64(len(text)),
		ContentHash:  hash,
		CreatedAt:    now,
		UpdatedAt:    now,
		ChunkCount:   len(pieces),
		Status:       StatusIndexed,
		Tags:         opts.Tags,
	}
	for i := range pieces {
		pieces[i].ID = fmt.Sprintf("%s#%04d", docID, i)
		pieces[i].DocumentID = docID
		pieces[i].CollectionID = collectionID
	}

	vectors, err := s.embed(ctx, pieces)
	if err != nil {
		// Embedding failure must not lose the document: fall back to lexical
		// only and record the reason so the user can retry indexing.
		doc.Status = StatusIndexed
		doc.Error = "向量化失败，仅支持关键词检索：" + err.Error()
		vectors = make([][]float32, len(pieces))
	}

	if err := s.persist(doc, pieces, vectors); err != nil {
		return Document{}, err
	}
	for i, p := range pieces {
		var vec []float32
		if i < len(vectors) {
			vec = vectors[i]
		}
		s.index.Add(p, vec)
	}
	return doc, nil
}

// AddFile reads a file from disk and indexes it.
func (s *Store) AddFile(ctx context.Context, collectionID, path string, opts IngestOptions) (Document, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	parsed, err := ParseFile(abs)
	if err != nil {
		return Document{}, err
	}
	return s.AddParsed(ctx, collectionID, parsed, abs, "file", opts)
}

// AddDirectory walks a directory and indexes every supported file. Progress is
// reported through onProgress, which may be nil.
func (s *Store) AddDirectory(ctx context.Context, collectionID, dir string, recursive bool, onProgress func(done, total int, current string)) (added int, failed []string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0, nil, err
	}

	var files []string
	walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != abs && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build") {
				return filepath.SkipDir
			}
			if !recursive && path != abs {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if !Supported(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return 0, nil, walkErr
	}

	total := len(files)
	for i, f := range files {
		select {
		case <-ctx.Done():
			return added, failed, ctx.Err()
		default:
		}
		if onProgress != nil {
			onProgress(i, total, f)
		}
		if _, err := s.AddFile(ctx, collectionID, f, IngestOptions{Replace: true}); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		added++
	}
	if onProgress != nil {
		onProgress(total, total, "")
	}
	return added, failed, nil
}

// AddURL fetches a web page and indexes its readable text.
func (s *Store) AddURL(ctx context.Context, collectionID, rawURL string, opts IngestOptions) (Document, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Document{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HitK/0.1; +https://github.com/hitk)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Document{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Document{}, fmt.Errorf("抓取失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Document{}, err
	}

	ct := resp.Header.Get("Content-Type")
	var parsed *ParsedDocument
	if strings.Contains(ct, "html") || strings.Contains(strings.ToLower(rawURL), ".htm") {
		parsed = ParseHTMLPage(rawURL, string(body))
	} else {
		parsed = ParseText(rawURL, string(body))
	}
	if opts.Title == "" {
		opts.Title = parsed.Title
	}
	return s.AddParsed(ctx, collectionID, parsed, rawURL, "url", opts)
}

// FindBySource looks up a document by its source path or URL.
func (s *Store) FindBySource(collectionID, source string) (Document, bool) {
	var found Document
	ok := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktDocuments).ForEach(func(_, v []byte) error {
			var d Document
			if json.Unmarshal(v, &d) != nil {
				return nil
			}
			if d.CollectionID == collectionID && d.Source == source {
				found = d
				ok = true
			}
			return nil
		})
	})
	return found, ok
}

// Reindex rebuilds vectors for every chunk. Called after the embedding model
// changes from an empty or different one.
func (s *Store) Reindex(ctx context.Context, onProgress func(done, total int)) error {
	type item struct {
		chunk Chunk
		vec   []float32
	}
	var items []item
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktChunks).ForEach(func(_, v []byte) error {
			var c Chunk
			if json.Unmarshal(v, &c) == nil {
				items = append(items, item{chunk: c})
			}
			return nil
		})
	})
	if err != nil {
		return err
	}

	const batch = 32
	for i := 0; i < len(items); i += batch {
		end := i + batch
		if end > len(items) {
			end = len(items)
		}
		chunks := make([]Chunk, 0, end-i)
		for _, it := range items[i:end] {
			chunks = append(chunks, it.chunk)
		}
		vecs, err := s.embed(ctx, chunks)
		if err != nil {
			return err
		}
		for j := range chunks {
			if j < len(vecs) {
				items[i+j].vec = vecs[j]
			}
		}
		if onProgress != nil {
			onProgress(end, len(items))
		}
	}

	if err := s.db.Update(func(tx *bolt.Tx) error {
		vb := tx.Bucket(bktVectors)
		for _, it := range items {
			if it.vec == nil {
				continue
			}
			if err := vb.Put([]byte(it.chunk.ID), encodeVector(it.vec)); err != nil {
				return err
			}
		}
		return metaPut(tx, "embedder", s.EmbedderID())
	}); err != nil {
		return err
	}
	return s.rebuildIndex()
}

// persist writes a document, its chunks and its vectors in one transaction.
func (s *Store) persist(doc Document, chunks []Chunk, vectors [][]float32) error {
	s.mu.RLock()
	remote := s.embedRemote
	s.mu.RUnlock()

	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bktDocuments).Put([]byte(doc.ID), raw); err != nil {
			return err
		}

		cb := tx.Bucket(bktChunks)
		vb := tx.Bucket(bktVectors)
		ids := make([]string, 0, len(chunks))
		for i, c := range chunks {
			craw, err := json.Marshal(c)
			if err != nil {
				return err
			}
			if err := cb.Put([]byte(c.ID), craw); err != nil {
				return err
			}
			ids = append(ids, c.ID)
			// Persist vectors only when they are expensive to recompute.
			if remote && i < len(vectors) && vectors[i] != nil {
				if err := vb.Put([]byte(c.ID), encodeVector(vectors[i])); err != nil {
					return err
				}
			}
		}
		idraw, _ := json.Marshal(ids)
		return tx.Bucket(bktDocChunks).Put([]byte(doc.ID), idraw)
	})
}

// embed computes vectors for chunks using the configured embedder. When no
// remote embedder is configured the offline hashed model is used so retrieval
// always has a vector signal.
func (s *Store) embed(ctx context.Context, chunks []Chunk) ([][]float32, error) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		if c.Heading != "" {
			texts[i] = c.Heading + "\n" + c.Text
		} else {
			texts[i] = c.Text
		}
	}

	s.mu.RLock()
	e := s.embedder
	s.mu.RUnlock()

	if e != nil {
		vecs, err := e.Embed(ctx, texts)
		if err == nil && len(vecs) == len(texts) {
			return vecs, nil
		}
		// Remote failure falls through to the local model rather than
		// abandoning the ingest.
		if err != nil {
			local := newLocalEmbedder()
			out, _ := local.Embed(ctx, texts)
			return out, nil
		}
	}
	local := newLocalEmbedder()
	return local.Embed(ctx, texts)
}

// rebuildIndex reconstructs the in-memory index from persisted chunks.
func (s *Store) rebuildIndex() error {
	idx := NewIndex()
	var pending []Chunk
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktChunks).ForEach(func(_, v []byte) error {
			var c Chunk
			if err := json.Unmarshal(v, &c); err != nil {
				return nil
			}
			pending = append(pending, c)
			return nil
		})
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	local := newLocalEmbedder()
	for _, c := range pending {
		vec, ok := s.loadVector(c.ID)
		if !ok {
			vecs, err := local.Embed(ctx, []string{c.Text})
			if err == nil && len(vecs) == 1 {
				vec = vecs[0]
			}
		}
		idx.Add(c, vec)
	}

	s.mu.Lock()
	s.index = idx
	s.mu.Unlock()
	return nil
}

// loadVector reads a persisted embedding.
func (s *Store) loadVector(chunkID string) ([]float32, bool) {
	var vec []float32
	_ = s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bktVectors).Get([]byte(chunkID))
		if raw == nil {
			return nil
		}
		vec = decodeVector(raw)
		return nil
	})
	return vec, vec != nil
}

// Stats summarizes the whole store.
func (s *Store) Stats() Stats {
	var st Stats
	_ = s.db.View(func(tx *bolt.Tx) error {
		st.Collections = tx.Bucket(bktCollections).Stats().KeyN
		_ = tx.Bucket(bktDocuments).ForEach(func(_, v []byte) error {
			var d Document
			if json.Unmarshal(v, &d) == nil {
				st.Documents++
				st.SizeBytes += d.SizeBytes
			}
			return nil
		})
		st.Chunks = tx.Bucket(bktChunks).Stats().KeyN
		return nil
	})
	st.Indexed = s.index.Len() > 0
	return st
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// Search runs hybrid retrieval for a single query.
func (s *Store) Search(ctx context.Context, query string, opts SearchOptions) ([]Hit, error) {
	return s.SearchExpanded(ctx, []string{query}, opts)
}

// SearchExpanded runs hybrid retrieval for a set of query variants and fuses
// the results. This is the entry point the agent uses: it issues a literal
// query plus any expansions, so a hit found by several variants outranks one
// found by only one.
func (s *Store) SearchExpanded(ctx context.Context, queries []string, opts SearchOptions) ([]Hit, error) {
	queries = nonEmpty(queries)
	if len(queries) == 0 {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	if opts.LexicalWeight == 0 && opts.VectorWeight == 0 {
		opts.LexicalWeight, opts.VectorWeight = 0.55, 0.45
	}

	filter := map[string]bool{}
	for _, c := range opts.Collections {
		if c = strings.TrimSpace(c); c != "" {
			filter[c] = true
		}
	}
	if len(filter) == 0 {
		filter = nil
	} else if resolved, err := s.resolveCollectionIDs(filter); err == nil {
		filter = resolved
	}

	// bestScore keeps the strongest contribution per chunk; votes counts how
	// many query variants surfaced it.
	bestScore := map[int32]float64{}
	votes := map[int32]int{}
	lexPart := map[int32]float64{}
	vecPart := map[int32]float64{}
	matched := map[int32]string{}

	for _, q := range queries {
		lex := s.index.Lexical(q, filter)
		lexNorm := normalizeScores(lex)
		for idx, sc := range lexNorm {
			if sc > bestScore[idx] {
				bestScore[idx] = opts.LexicalWeight * sc
				matched[idx] = q
			}
			lexPart[idx] = math.Max(lexPart[idx], sc)
			votes[idx]++
		}

		vecs, err := s.embedQuery(ctx, q)
		if err != nil || len(vecs) == 0 {
			continue
		}
		vec := s.index.Vector(vecs[0], filter)
		// Cosine on the local hashed model rarely reaches 1, so re-scale the
		// observed range instead of dividing by a theoretical maximum.
		vecNorm := normalizeScores(vec)
		for idx, sc := range vecNorm {
			cand := opts.LexicalWeight*lexPart[idx] + opts.VectorWeight*sc
			if cand > bestScore[idx] {
				bestScore[idx] = cand
				matched[idx] = q
			}
			vecPart[idx] = math.Max(vecPart[idx], sc)
			votes[idx]++
		}
	}

	hits := make([]Hit, 0, len(bestScore))
	for idx, score := range bestScore {
		// Agreement across query variants is evidence of relevance, so a chunk
		// found by several expansions gets a small boost.
		if v := votes[idx]; v > 1 {
			score *= 1 + 0.05*float64(v-1)
		}
		if score < opts.MinScore {
			continue
		}
		ic, ok := s.index.chunkAt(idx)
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			ChunkID:      ic.chunkID,
			DocumentID:   ic.documentID,
			CollectionID: ic.collectionID,
			Ordinal:      ic.ordinal,
			Heading:      ic.heading,
			Text:         clip(ic.text, opts.SnippetRunes),
			Score:        round4(score),
			Lexical:      round4(lexPart[idx]),
			Vector:       round4(vecPart[idx]),
			MatchedQuery: matched[idx],
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ChunkID < hits[j].ChunkID
	})
	if len(hits) > opts.Limit {
		hits = hits[:opts.Limit]
	}
	return s.decorate(hits), nil
}

// decorate fills in the document-level fields the UI shows.
func (s *Store) decorate(hits []Hit) []Hit {
	if len(hits) == 0 {
		return hits
	}
	docs := map[string]Document{}
	cols := map[string]string{}
	_ = s.db.View(func(tx *bolt.Tx) error {
		db := tx.Bucket(bktDocuments)
		cb := tx.Bucket(bktCollections)
		for i := range hits {
			if _, ok := docs[hits[i].DocumentID]; !ok {
				if raw := db.Get([]byte(hits[i].DocumentID)); raw != nil {
					var d Document
					if json.Unmarshal(raw, &d) == nil {
						docs[hits[i].DocumentID] = d
					}
				}
			}
			if _, ok := cols[hits[i].CollectionID]; !ok {
				if raw := cb.Get([]byte(hits[i].CollectionID)); raw != nil {
					var c Collection
					if json.Unmarshal(raw, &c) == nil {
						cols[hits[i].CollectionID] = c.Name
					}
				}
			}
		}
		return nil
	})
	for i := range hits {
		if d, ok := docs[hits[i].DocumentID]; ok {
			hits[i].DocTitle = d.Title
			hits[i].Source = d.Source
			hits[i].Kind = d.Kind
		}
		hits[i].Collection = cols[hits[i].CollectionID]
	}
	return hits
}

// resolveCollectionIDs maps user-supplied names or ids to ids.
func (s *Store) resolveCollectionIDs(in map[string]bool) (map[string]bool, error) {
	out := map[string]bool{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bktCollections).ForEach(func(k, v []byte) error {
			var c Collection
			if json.Unmarshal(v, &c) != nil {
				return nil
			}
			if in[string(k)] || in[c.Name] {
				out[string(k)] = true
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return in, nil
	}
	return out, nil
}

// embedQuery embeds one search string, reusing the ingest embedder.
func (s *Store) embedQuery(ctx context.Context, q string) ([][]float32, error) {
	s.mu.RLock()
	e := s.embedder
	s.mu.RUnlock()
	if e != nil {
		if vecs, err := e.Embed(ctx, []string{q}); err == nil && len(vecs) == 1 {
			return vecs, nil
		}
	}
	return newLocalEmbedder().Embed(ctx, []string{q})
}

// normalizeScores scales a ranked list into [0,1] by its top score. BM25 and
// cosine live on different scales, so fusion needs them commensurate.
func normalizeScores(list []lexicalScore) map[int32]float64 {
	out := make(map[int32]float64, len(list))
	if len(list) == 0 {
		return out
	}
	top := list[0].score
	if top <= 0 {
		return out
	}
	for _, s := range list {
		if s.score <= 0 {
			continue
		}
		out[s.idx] = s.score / top
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:12])
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

func metaPut(tx *bolt.Tx, key, value string) error {
	return tx.Bucket(bktMeta).Put([]byte(key), []byte(value))
}

// MetaGet reads a metadata value.
func (s *Store) MetaGet(key string) string {
	var v string
	_ = s.db.View(func(tx *bolt.Tx) error {
		v = string(tx.Bucket(bktMeta).Get([]byte(key)))
		return nil
	})
	return v
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func clip(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func round4(f float64) float64 {
	return math.Round(f*10000) / 10000
}
