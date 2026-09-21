package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChunkerSplitsAndOverlaps verifies the chunker respects its size budget and
// keeps markdown headings attached to the chunks beneath them.
func TestChunkerSplitsAndOverlaps(t *testing.T) {
	c := NewChunker(300, 60)
	var b strings.Builder
	b.WriteString("# 第一章 总览\n\n")
	for i := 0; i < 12; i++ {
		b.WriteString("这是第一段内容，用于测试分块。HitK 会把长文档切分成带重叠的片段。\n\n")
	}
	b.WriteString("## 1.1 细节\n\n")
	b.WriteString("细节段落的正文，应该带上二级标题。\n")

	chunks := c.Split(b.String(), "总览")
	if len(chunks) < 2 {
		t.Fatalf("expected the document to split into multiple chunks, got %d", len(chunks))
	}
	for i, ch := range chunks {
		if ch.Ordinal != i {
			t.Errorf("chunk %d has ordinal %d", i, ch.Ordinal)
		}
		if strings.TrimSpace(ch.Text) == "" {
			t.Errorf("chunk %d is empty", i)
		}
		if ch.TokenCount == 0 {
			t.Errorf("chunk %d has zero token estimate", i)
		}
	}

	found := false
	for _, ch := range chunks {
		if ch.Heading == "1.1 细节" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no chunk carried the second heading; heading tracking is broken")
	}
}

// TestChunkerKeepsCodeFenceIntact ensures a fenced code block is never split
// mid-function, because a half-function chunk is useless to the model.
func TestChunkerKeepsCodeFenceIntact(t *testing.T) {
	c := NewChunker(400, 50)
	doc := "# 示例\n\n说明文字。\n\n```go\nfunc main() {\n    println(\"hello\")\n    println(\"world\")\n}\n```\n\n收尾。\n"

	chunks := c.Split(doc, "示例")
	joined := ""
	for _, ch := range chunks {
		joined += ch.Text + "\n"
	}
	if !strings.Contains(joined, "func main() {") || !strings.Contains(joined, "println(\"world\")") {
		t.Fatalf("code fence was split apart:\n%s", joined)
	}
}

// TestEstimateTokens asserts CJK text is not wildly under-counted, which would
// let the context budget overflow before compaction kicks in.
func TestEstimateTokens(t *testing.T) {
	cjk := strings.Repeat("知识库", 100) // 300 characters
	if n := EstimateTokens(cjk); n < 200 || n > 400 {
		t.Errorf("CJK estimate out of range: %d", n)
	}
	latin := strings.Repeat("knowledge ", 100) // ~1000 chars
	if n := EstimateTokens(latin); n < 150 || n > 400 {
		t.Errorf("latin estimate out of range: %d", n)
	}
	if EstimateTokens("") != 0 {
		t.Error("empty string should estimate to zero tokens")
	}
}

// newTestStore opens a throwaway knowledge base on disk.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "kb.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestStoreIngestAndSearch covers the whole retrieval path: create a collection,
// ingest Chinese and English documents, then assert that hybrid search finds the
// right one for a query in either language.
func TestStoreIngestAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	col, err := s.CreateCollection("测试库", "单元测试")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	docs := map[string]string{
		"hitk-设计.md": `# HitK 设计

## 检索策略
HitK 使用 BM25 关键词检索与向量语义检索的混合排序，通过归一化后加权融合，
并用多轮改写检索式的方式提升召回率。引用来源会标注文档标题。

## 分块策略
长文档按段落切分，保留 markdown 标题，并带 120 字的上下文重叠。`,
		"hitk-architecture.md": `# HitK Architecture

## Retrieval
HitK performs hybrid retrieval combining BM25 lexical scoring with vector
similarity, fusing the two rankings after normalisation. The agent expands the
query into several paraphrases and fuses the results.

## Protocol
The agent protocol follows a submission/event split: the client submits an Op,
the runtime streams EventMsg values carrying turn and item lifecycle events.`,
	}

	for name, body := range docs {
		doc, err := s.AddParsed(ctx, col.ID, ParseText(name, body), "test://"+name, "text",
			IngestOptions{Title: name})
		if err != nil {
			t.Fatalf("ingest %s: %v", name, err)
		}
		if doc.ChunkCount == 0 {
			t.Fatalf("ingest %s produced no chunks", name)
		}
	}

	// Chinese query should surface the Chinese document.
	hits, err := s.Search(ctx, "混合检索是怎么排序的", SearchOptions{Limit: 5, SnippetRunes: 200})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Chinese query returned no hits")
	}
	if !strings.Contains(hits[0].DocTitle, "设计") {
		t.Errorf("expected the Chinese doc to rank first, got %q", hits[0].DocTitle)
	}

	// English query should surface the English document.
	hits, err = s.Search(ctx, "submission event protocol", SearchOptions{Limit: 5, SnippetRunes: 200})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("English query returned no hits")
	}
	if !strings.Contains(hits[0].DocTitle, "architecture") {
		t.Errorf("expected the English doc to rank first, got %q", hits[0].DocTitle)
	}

	// Expanded search should never rank worse than the single-query form.
	expanded, err := s.SearchExpanded(ctx, []string{"混合检索", "hybrid retrieval", "排序融合"},
		SearchOptions{Limit: 5, SnippetRunes: 200})
	if err != nil {
		t.Fatalf("expanded search: %v", err)
	}
	if len(expanded) == 0 {
		t.Fatal("expanded search returned no hits")
	}
	if expanded[0].Score < hits[0].Score {
		t.Errorf("expanded search top score %.4f below single-query %.4f",
			expanded[0].Score, hits[0].Score)
	}
}

// TestStoreChunksAndDelete checks that a document's chunks are retrievable in
// order and that deleting the document removes them from the index too.
func TestStoreChunksAndDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	col, err := s.CreateCollection("删除测试", "")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	body := strings.Repeat("这是一段用于测试删除的内容。\n\n", 60)
	doc, err := s.AddParsed(ctx, col.ID, ParseText("长文", body), "test://long", "text", IngestOptions{Title: "长文"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	chunks, err := s.Chunks(doc.ID)
	if err != nil {
		t.Fatalf("chunks: %v", err)
	}
	if len(chunks) != doc.ChunkCount {
		t.Errorf("chunk count mismatch: stored %d, got %d", doc.ChunkCount, len(chunks))
	}
	for i, ch := range chunks {
		if ch.Ordinal != i {
			t.Errorf("chunk %d out of order: ordinal %d", i, ch.Ordinal)
		}
	}

	before := s.Stats().Chunks
	if err := s.DeleteDocument(doc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if after := s.Stats().Chunks; after != 0 {
		t.Errorf("expected 0 chunks after delete, got %d (was %d)", after, before)
	}
	if _, err := s.Chunk(chunks[0].ID); err == nil {
		t.Error("deleted chunk is still readable")
	}
}

// TestStoreRejectsDuplicateCollectionName guards the user-facing invariant that
// names are unique, since the agent addresses collections by name.
func TestStoreRejectsDuplicateCollectionName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateCollection("项目文档", ""); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.CreateCollection("项目文档", ""); err == nil {
		t.Fatal("duplicate collection name was accepted")
	}
}

// TestStoreSkippedReasonsFlowThrough verifies that a file the parser cannot read
// produces an actionable error rather than an empty document.
func TestStoreSkippedReasonsFlowThrough(t *testing.T) {
	s := newTestStore(t)
	col, err := s.CreateCollection("跳过测试", "")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	_, err = s.AddParsed(context.Background(), col.ID,
		&ParsedDocument{Title: "扫描件", SkippedReason: "PDF 中未提取到文本（可能是扫描件，需 OCR）"},
		"test://scan", "file", IngestOptions{})
	if err == nil {
		t.Fatal("expected an error for a document with a skipped reason")
	}
	if !strings.Contains(err.Error(), "OCR") {
		t.Errorf("error lost the skipped reason: %v", err)
	}
}

// TestParseFileReadsMarkdown exercises the on-disk ingest path end to end.
func TestParseFileReadsMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	content := "# 笔记\n\n知识库应当支持 Markdown。\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	parsed, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Title != "notes" {
		t.Errorf("title = %q, want %q", parsed.Title, "notes")
	}
	if !strings.Contains(parsed.Text, "Markdown") {
		t.Errorf("text missing content: %q", parsed.Text)
	}
}

// TestParseCSVToMarkdown checks a table becomes something the model can read.
func TestParseCSVToMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("name,score\n甲,90\n乙,85\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	parsed, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(parsed.Text, "| name | score |") {
		t.Errorf("CSV was not rendered as a markdown table:\n%s", parsed.Text)
	}
	if !strings.Contains(parsed.Text, "甲") {
		t.Error("CSV content missing")
	}
}

// TestStripHTMLRemovesScripts makes sure a fetched page cannot smuggle script or
// style bodies into the index.
func TestStripHTMLRemovesScripts(t *testing.T) {
	html := `<html><head><title>标题</title><style>body{color:red}</style>
	<script>console.log("secret")</script></head>
	<body><h1>正文标题</h1><p>第一段</p><p>第二段</p></body></html>`

	text := StripHTML(html)
	for _, banned := range []string{"console.log", "color:red", "<h1>"} {
		if strings.Contains(text, banned) {
			t.Errorf("stripped text still contains %q:\n%s", banned, text)
		}
	}
	for _, want := range []string{"正文标题", "第一段", "第二段"} {
		if !strings.Contains(text, want) {
			t.Errorf("stripped text missing %q:\n%s", want, text)
		}
	}
}
