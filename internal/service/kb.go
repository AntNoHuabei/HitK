package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
)

// KBService exposes knowledge-base management to the frontend.
type KBService struct{ c *Container }

// NewKBService builds the service.
func NewKBService(c *Container) *KBService { return &KBService{c: c} }

// ServiceName implements [application.ServiceName].
func (*KBService) ServiceName() string { return "KB" }

// Collections lists knowledge bases.
func (s *KBService) Collections() ([]kb.Collection, error) {
	return s.c.KB.Collections()
}

// CreateCollection adds a knowledge base.
func (s *KBService) CreateCollection(name, description string) (kb.Collection, error) {
	return s.c.KB.CreateCollection(name, description)
}

// UpdateCollection renames or re-describes a knowledge base.
func (s *KBService) UpdateCollection(id, name, description string) (kb.Collection, error) {
	return s.c.KB.UpdateCollection(id, name, description)
}

// DeleteCollection removes a knowledge base and everything in it.
func (s *KBService) DeleteCollection(id string) error {
	return s.c.KB.DeleteCollection(id)
}

// Documents lists a knowledge base's documents.
func (s *KBService) Documents(collectionID string) ([]kb.Document, error) {
	return s.c.KB.Documents(collectionID)
}

// DeleteDocument removes one document.
func (s *KBService) DeleteDocument(id string) error {
	return s.c.KB.DeleteDocument(id)
}

// Stats summarizes the store for the sidebar footer.
func (s *KBService) Stats() kb.Stats {
	return s.c.KB.Stats()
}

// EmbedderID reports the active embedding model.
func (s *KBService) EmbedderID() string {
	return s.c.KB.EmbedderID()
}

// ImportResult reports what an ingest run did.
type ImportResult struct {
	Added   int      `json:"added"`
	Failed  []string `json:"failed"`
	Skipped int      `json:"skipped"`
	Message string   `json:"message"`
}

// AddFiles ingests an explicit list of file paths.
func (s *KBService) AddFiles(collectionID string, paths []string) (ImportResult, error) {
	res := ImportResult{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !s.c.Documents.Supported(p) {
			res.Skipped++
			continue
		}
		if _, err := s.c.KB.AddFile(ctx, collectionID, p, kb.IngestOptions{Replace: true}); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		res.Added++
	}
	res.Message = summarizeImport(res)
	return res, nil
}

// AddDirectory walks a folder and ingests every supported file.
func (s *KBService) AddDirectory(collectionID, dir string, recursive bool) (ImportResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	added, failed, err := s.c.KB.AddDirectory(ctx, collectionID, dir, recursive, func(done, total int, current string) {
		// Progress is reported on the notice channel so the import dialog can
		// show a live counter without a second event type.
		s.c.emit(progressEvent(total, done, current))
	})
	if err != nil && added == 0 {
		return ImportResult{}, err
	}
	res := ImportResult{Added: added, Failed: failed}
	res.Message = summarizeImport(res)
	return res, nil
}

// AddURL ingests a web page.
func (s *KBService) AddURL(collectionID, url string) (ImportResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	doc, err := s.c.KB.AddURL(ctx, collectionID, url, kb.IngestOptions{Replace: true})
	if err != nil {
		return ImportResult{Failed: []string{err.Error()}, Message: err.Error()}, nil
	}
	return ImportResult{Added: 1, Message: fmt.Sprintf("已导入《%s》，%d 个分块", doc.Title, doc.ChunkCount)}, nil
}

// AddText ingests pasted content.
func (s *KBService) AddText(collectionID, title, text string) (ImportResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if strings.TrimSpace(text) == "" {
		return ImportResult{}, fmt.Errorf("内容不能为空")
	}
	if strings.TrimSpace(title) == "" {
		title = "粘贴内容"
	}
	doc, err := s.c.KB.AddParsed(ctx, collectionID, kb.ParseText(title, text),
		"text://"+title, "text", kb.IngestOptions{Title: title})
	if err != nil {
		return ImportResult{Failed: []string{err.Error()}, Message: err.Error()}, nil
	}
	return ImportResult{Added: 1, Message: fmt.Sprintf("已写入《%s》，%d 个分块", doc.Title, doc.ChunkCount)}, nil
}

// SearchHitView is a search result shaped for the UI.
type SearchHitView struct {
	ChunkID    string  `json:"chunkId"`
	DocumentID string  `json:"documentId"`
	DocTitle   string  `json:"docTitle"`
	Collection string  `json:"collection"`
	Source     string  `json:"source"`
	Heading    string  `json:"heading,omitempty"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
	Lexical    float64 `json:"lexical"`
	Vector     float64 `json:"vector"`
}

// Search runs a retrieval for the knowledge-base search box. It exercises the
// exact same hybrid path the agent uses, so what a user sees here is what the
// model will see.
func (s *KBService) Search(query, collectionID string, limit int) ([]SearchHitView, error) {
	if strings.TrimSpace(query) == "" {
		return []SearchHitView{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := kb.SearchOptions{Limit: limit, SnippetRunes: 320, MinScore: 0.05}
	if collectionID != "" {
		opts.Collections = []string{collectionID}
	}
	hits, err := s.c.KB.Search(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHitView, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHitView{
			ChunkID:    h.ChunkID,
			DocumentID: h.DocumentID,
			DocTitle:   h.DocTitle,
			Collection: h.Collection,
			Source:     h.Source,
			Heading:    h.Heading,
			Snippet:    h.Text,
			Score:      h.Score,
			Lexical:    h.Lexical,
			Vector:     h.Vector,
		})
	}
	return out, nil
}

// DocumentContent returns a document's full text, reconstructed from its
// chunks, for the reader pane.
func (s *KBService) DocumentContent(documentID string) (string, error) {
	chunks, err := s.c.KB.Chunks(documentID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, ch := range chunks {
		if ch.Heading != "" {
			fmt.Fprintf(&b, "## %s\n\n", ch.Heading)
		}
		b.WriteString(ch.Text)
		b.WriteString("\n\n")
	}
	return b.String(), nil
}

// Reindex rebuilds every chunk's embedding. Needed after switching embedding
// models, since vectors from different models are not comparable.
func (s *KBService) Reindex() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	return s.c.KB.Reindex(ctx, func(done, total int) {
		s.c.emit(progressEvent(total, done, "重建索引"))
	})
}

// SupportedExtensions lists the ingestable file types for the import dialog.
func (s *KBService) SupportedExtensions() []string {
	return s.c.Documents.SupportedExtensions()
}

func summarizeImport(res ImportResult) string {
	var parts []string
	if res.Added > 0 {
		parts = append(parts, fmt.Sprintf("成功导入 %d 个", res.Added))
	}
	if res.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 个不支持的文件", res.Skipped))
	}
	if len(res.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 个", len(res.Failed)))
	}
	if len(parts) == 0 {
		return "没有可导入的文件"
	}
	return strings.Join(parts, "，")
}

// progressEvent wraps an import counter in a notice event so the UI's import
// dialog can render live progress without a dedicated event type.
func progressEvent(total, done int, current string) *protocol.Event {
	ev := protocol.NewEvent(protocol.EvNotice)
	ev.Notice = fmt.Sprintf("%d/%d %s", done, total, current)
	ev.StepIndex = done
	ev.Thread = nil
	ev.Usage = &protocol.Usage{TotalTokens: total, Steps: done}
	return ev
}
