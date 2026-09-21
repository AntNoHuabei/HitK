// Package kb implements HitK's knowledge bases and the retrieval layer behind
// Agentic Search.
//
// A collection is a named set of documents. Each document is parsed to plain
// text, split into overlapping chunks, and stored with two indexes:
//
//	lexical  — an in-memory BM25 inverted index rebuilt from stored chunks
//	vector   — one embedding per chunk, from a remote endpoint when configured
//	           and from the offline hashed bag-of-features model otherwise
//
// Queries run both and fuse the rankings, which is what lets a Chinese query
// find an English document and vice versa while still rewarding exact term
// matches. The agent drives the loop: it expands the query, searches, reads the
// winning chunks, and searches again when the first pass is thin.
package kb

import "time"

// Collection is a named knowledge base.
type Collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	DocCount    int    `json:"docCount"`
	ChunkCount  int    `json:"chunkCount"`
	SizeBytes   int64  `json:"sizeBytes"`
	// EmbedderID records which embedding model indexed this collection, so a
	// model change can be detected and the collection re-indexed.
	EmbedderID string `json:"embedderId,omitempty"`
	// ReadOnly collections are shipped samples the user cannot modify.
	ReadOnly bool `json:"readOnly,omitempty"`
}

// DocumentStatus is the ingestion state of a document.
type DocumentStatus string

const (
	StatusPending DocumentStatus = "pending"
	StatusIndexed DocumentStatus = "indexed"
	StatusFailed  DocumentStatus = "failed"
)

// Document is one ingested source.
type Document struct {
	ID           string `json:"id"`
	CollectionID string `json:"collectionId"`
	Title        string `json:"title"`
	// Source is the absolute filesystem path or URL the document came from.
	Source string `json:"source"`
	// Kind is file | url | text.
	Kind      string `json:"kind"`
	Mime      string `json:"mime,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	// ContentHash lets re-importing the same unchanged file be a no-op.
	ContentHash string         `json:"contentHash,omitempty"`
	CreatedAt   int64          `json:"createdAt"`
	UpdatedAt   int64          `json:"updatedAt"`
	ChunkCount  int            `json:"chunkCount"`
	Status      DocumentStatus `json:"status"`
	Error       string         `json:"error,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
}

// Chunk is one retrieval unit.
type Chunk struct {
	ID           string `json:"id"`
	DocumentID   string `json:"documentId"`
	CollectionID string `json:"collectionId"`
	Ordinal      int    `json:"ordinal"`
	Text         string `json:"text"`
	// Heading is the nearest preceding markdown heading, used both for display
	// and to give the model a cheap map of the document.
	Heading    string `json:"heading,omitempty"`
	TokenCount int    `json:"tokenCount"`
}

// Hit is one search result.
type Hit struct {
	ChunkID      string  `json:"chunkId"`
	DocumentID   string  `json:"documentId"`
	CollectionID string  `json:"collectionId"`
	Collection   string  `json:"collection"`
	DocTitle     string  `json:"docTitle"`
	Source       string  `json:"source"`
	Kind         string  `json:"kind"`
	Ordinal      int     `json:"ordinal"`
	Heading      string  `json:"heading,omitempty"`
	Text         string  `json:"text"`
	Score        float64 `json:"score"`
	Lexical      float64 `json:"lexical"`
	Vector       float64 `json:"vector"`
	// MatchedQuery records which expanded query surfaced this hit, which the
	// agent uses to decide whether another search round is worthwhile.
	MatchedQuery string `json:"matchedQuery,omitempty"`
}

// Stats summarizes a collection for the UI header.
type Stats struct {
	Collections int   `json:"collections"`
	Documents   int   `json:"documents"`
	Chunks      int   `json:"chunks"`
	SizeBytes   int64 `json:"sizeBytes"`
	Indexed     bool  `json:"indexed"`
}

// SearchOptions tunes a retrieval call.
type SearchOptions struct {
	// Collections restricts the search; empty means every collection.
	Collections []string
	// Limit caps returned hits.
	Limit int
	// SnippetRunes truncates each hit's text. Zero means no truncation.
	SnippetRunes int
	// LexicalWeight and VectorWeight control the fusion blend; zero values
	// fall back to 0.55 / 0.45.
	LexicalWeight float64
	VectorWeight  float64
	// MinScore drops weak hits so the agent is not handed noise.
	MinScore float64
}

// docRecord is the parsed content of a document held between parse and index.
type docRecord struct {
	doc    Document
	chunks []Chunk
}

// nowMS is a small helper so every timestamp in a record comes from one call.
func nowMS() int64 { return time.Now().UnixMilli() }
