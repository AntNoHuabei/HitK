package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
)

// KBCollections lets the agent discover what it can search.
type KBCollections struct{}

// Name implements [Tool].
func (*KBCollections) Name() string { return "kb_collections" }

// DisplayName implements [Tool].
func (*KBCollections) DisplayName() string { return "列出知识库" }

// Description implements [Tool].
func (*KBCollections) Description() string {
	return "列出所有可检索的知识库及其文档数量。在调用 kb_search 之前先调用它，确认可用的知识库名称，避免在没有内容的库里空搜。"
}

// Schema implements [Tool].
func (*KBCollections) Schema() json.RawMessage {
	return SchemaFor(nil, map[string]string{})
}

// Mutating implements [Tool].
func (*KBCollections) Mutating() bool { return false }

// Run implements [Tool].
func (*KBCollections) Run(ctx *Context, _ json.RawMessage) (Result, error) {
	if ctx.KB == nil {
		return Result{}, fmt.Errorf("知识库未初始化")
	}
	cols, err := ctx.KB.Collections()
	if err != nil {
		return Result{}, err
	}
	if len(cols) == 0 {
		return Result{
			Content: "当前没有任何知识库。请提示用户先在左侧「知识库」中添加文档，或使用 kb_add 工具从 URL 添加内容。",
			Display: "0 个知识库",
		}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个知识库：\n", len(cols))
	for _, c := range cols {
		desc := ""
		if c.Description != "" {
			desc = " — " + c.Description
		}
		fmt.Fprintf(&b, "- %s（id=%s）：%d 个文档 / %d 个分块%s\n", c.Name, c.ID, c.DocCount, c.ChunkCount, desc)
	}
	return Result{Content: b.String(), Display: fmt.Sprintf("%d 个知识库", len(cols))}, nil
}

// KBSearch runs hybrid retrieval. This is the core of Agentic Search: the
// agent may call it repeatedly with refined wording, and each call reports how
// strong the evidence was so the model can decide whether to broaden, narrow,
// or admit the knowledge base does not cover the question.
type KBSearch struct{}

// Name implements [Tool].
func (*KBSearch) Name() string { return "kb_search" }

// DisplayName implements [Tool].
func (*KBSearch) DisplayName() string { return "检索知识库" }

// Description implements [Tool].
func (*KBSearch) Description() string {
	return "在知识库中做混合检索（关键词 BM25 + 向量语义），返回带分块 id 的原文片段。用法要点：\n" +
		"1) queries 可以传多个改写版本（同义改写、中英对照、关键词拆解），系统会融合排序，强烈建议一次传 2-4 个变体以提高召回；\n" +
		"2) 先用较小 limit 观察命中质量，若结果稀疏或分数低，再换关键词重搜，而不是直接回答；\n" +
		"3) 返回的 chunkId 可以交给 kb_open 展开阅读上下文；\n" +
		"4) 回答中引用到的内容必须来自检索结果，并在正文里标注来源文档标题。"
}

// Schema implements [Tool].
func (*KBSearch) Schema() json.RawMessage {
	return SchemaFor([]string{"queries"}, map[string]string{
		"queries":     `{"type":"array","items":{"type":"string"},"description":"一个或多个检索式（2-4 个改写变体效果最好）"}`,
		"collections": `{"type":"array","items":{"type":"string"},"description":"限定知识库名称或 id；留空表示全部知识库"}`,
		"limit":       `{"type":"integer","description":"返回条数，默认 8，最大 30"}`,
		"snippet":     `{"type":"integer","description":"每条结果截断的字数，默认 700"}`,
	})
}

// Mutating implements [Tool].
func (*KBSearch) Mutating() bool { return false }

// Run implements [Tool].
func (*KBSearch) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	if ctx.KB == nil {
		return Result{}, fmt.Errorf("知识库未初始化")
	}
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	queries := argStrings(args, "queries")
	if len(queries) == 0 {
		// A model that sends `query` instead of `queries` should still work.
		if q := argString(args, "query"); q != "" {
			queries = []string{q}
		}
	}
	if len(queries) == 0 {
		return Result{}, fmt.Errorf("queries 不能为空")
	}

	collections := argStrings(args, "collections")
	if len(collections) == 0 && len(ctx.KnowledgeBases) > 0 {
		// A scoped turn should not silently search unrelated collections.
		collections = append([]string(nil), ctx.KnowledgeBases...)
	}

	limit := argInt(args, "limit", 8)
	if limit > 30 {
		limit = 30
	}
	snippet := argInt(args, "snippet", 700)

	hits, err := ctx.KB.SearchExpanded(ctx.Ctx, queries, kb.SearchOptions{
		Collections:  collections,
		Limit:        limit,
		SnippetRunes: snippet,
		MinScore:     0.12,
	})
	if err != nil {
		return Result{}, err
	}

	refs := make([]protocol.Reference, 0, len(hits))
	for _, h := range hits {
		refs = append(refs, protocol.Reference{
			Collection: h.Collection,
			DocID:      h.DocumentID,
			DocTitle:   h.DocTitle,
			ChunkID:    h.ChunkID,
			Path:       h.Source,
			Score:      h.Score,
			Snippet:    firstRunes(h.Text, 180),
		})
	}

	display := fmt.Sprintf("命中 %d 条", len(hits))
	if len(hits) == 0 {
		return Result{
			Content: fmt.Sprintf("未检索到与 %s 相关的内容。\n建议：换用更具体或更宽泛的关键词、尝试中英文对照表述，或确认相关文档已导入知识库。不要凭空回答该问题。",
				strings.Join(queries, " / ")),
			Display: "无命中",
		}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "检索式：%s\n命中 %d 条（score 为混合相关度，lex/vec 分别为关键词/语义分量）：\n\n",
		strings.Join(queries, " | "), len(hits))
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] chunkId=%s | 《%s》", i+1, h.ChunkID, h.DocTitle)
		if h.Collection != "" {
			fmt.Fprintf(&b, " | 知识库:%s", h.Collection)
		}
		if h.Heading != "" {
			fmt.Fprintf(&b, " | 小节:%s", h.Heading)
		}
		fmt.Fprintf(&b, " | score=%.3f (lex=%.3f vec=%.3f)\n", h.Score, h.Lexical, h.Vector)
		fmt.Fprintf(&b, "%s\n\n", h.Text)
	}
	return Result{Content: b.String(), Display: display, Refs: refs, Meta: map[string]any{"hits": len(hits)}}, nil
}

// KBOpen expands a chunk into surrounding context, and can also return a
// document's outline so the agent can navigate a long file.
type KBOpen struct{}

// Name implements [Tool].
func (*KBOpen) Name() string { return "kb_open" }

// DisplayName implements [Tool].
func (*KBOpen) DisplayName() string { return "阅读原文" }

// Description implements [Tool].
func (*KBOpen) Description() string {
	return "打开检索结果对应的原文上下文。给定 chunkId 时返回该分块及其前后各 window 个分块（用于确认细节、看完整表格或代码）；" +
		"给定 documentId 时返回整篇文档的分块目录（标题 + 序号 + chunkId），便于大范围浏览。"
}

// Schema implements [Tool].
func (*KBOpen) Schema() json.RawMessage {
	return SchemaFor(nil, map[string]string{
		"chunkId":    `{"type":"string","description":"kb_search 返回的 chunkId"}`,
		"documentId": `{"type":"string","description":"文档 id，返回其目录"}`,
		"window":     `{"type":"integer","description":"前后展开的分块数，默认 1，最大 5"}`,
	})
}

// Mutating implements [Tool].
func (*KBOpen) Mutating() bool { return false }

// Run implements [Tool].
func (*KBOpen) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	if ctx.KB == nil {
		return Result{}, fmt.Errorf("知识库未初始化")
	}
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	window := argInt(args, "window", 1)
	if window < 0 {
		window = 0
	}
	if window > 5 {
		window = 5
	}

	if chunkID := firstNonEmpty(argString(args, "chunkId"), argString(args, "chunk_id")); chunkID != "" {
		return openChunk(ctx, chunkID, window)
	}
	if docID := firstNonEmpty(argString(args, "documentId"), argString(args, "document_id")); docID != "" {
		return outlineDocument(ctx, docID)
	}
	return Result{}, fmt.Errorf("需要提供 chunkId 或 documentId")
}

func openChunk(ctx *Context, chunkID string, window int) (Result, error) {
	chunk, err := ctx.KB.Chunk(chunkID)
	if err != nil {
		return Result{}, err
	}
	doc, err := ctx.KB.Document(chunk.DocumentID)
	if err != nil {
		return Result{}, err
	}
	all, err := ctx.KB.Chunks(chunk.DocumentID)
	if err != nil {
		return Result{}, err
	}

	// Locate the requested chunk in ordinal order, then widen the slice.
	pos := 0
	for i, c := range all {
		if c.ID == chunkID {
			pos = i
			break
		}
	}
	lo := pos - window
	if lo < 0 {
		lo = 0
	}
	hi := pos + window + 1
	if hi > len(all) {
		hi = len(all)
	}
	selected := all[lo:hi]

	var b strings.Builder
	fmt.Fprintf(&b, "《%s》第 %d-%d 分块（共 %d 块）\n来源：%s\n\n",
		doc.Title, lo+1, hi, len(all), doc.Source)
	for _, c := range selected {
		marker := ""
		if c.ID == chunkID {
			marker = " ← 当前定位"
		}
		fmt.Fprintf(&b, "--- chunk %d [%s]%s ---\n", c.Ordinal+1, c.ID, marker)
		if c.Heading != "" {
			fmt.Fprintf(&b, "小节：%s\n", c.Heading)
		}
		fmt.Fprintf(&b, "%s\n\n", c.Text)
	}

	content, truncated := truncate(b.String(), 24<<10)
	return Result{
		Content:   content,
		Display:   fmt.Sprintf("《%s》%d 块", doc.Title, len(selected)),
		Truncated: truncated,
		Refs: []protocol.Reference{{
			Collection: doc.CollectionID,
			DocID:      doc.ID,
			DocTitle:   doc.Title,
			ChunkID:    chunkID,
			Path:       doc.Source,
			Score:      1,
			Snippet:    firstRunes(chunk.Text, 180),
		}},
	}, nil
}

func outlineDocument(ctx *Context, docID string) (Result, error) {
	doc, err := ctx.KB.Document(docID)
	if err != nil {
		return Result{}, err
	}
	chunks, err := ctx.KB.Chunks(docID)
	if err != nil {
		return Result{}, err
	}
	if len(chunks) == 0 {
		return Result{Content: "该文档没有分块。", Display: "空文档"}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "《%s》\n来源：%s\n分块数：%d\n\n目录：\n", doc.Title, doc.Source, len(chunks))
	for _, c := range chunks {
		heading := c.Heading
		if heading == "" {
			heading = firstRunes(strings.ReplaceAll(c.Text, "\n", " "), 60)
		}
		fmt.Fprintf(&b, "- #%d [%s] %s\n", c.Ordinal+1, c.ID, heading)
	}
	content, truncated := truncate(b.String(), 12<<10)
	return Result{Content: content, Display: fmt.Sprintf("《%s》目录", doc.Title), Truncated: truncated}, nil
}

// KBAdd ingests new material at the agent's initiative.
type KBAdd struct{}

// Name implements [Tool].
func (*KBAdd) Name() string { return "kb_add" }

// DisplayName implements [Tool].
func (*KBAdd) DisplayName() string { return "写入知识库" }

// Description implements [Tool].
func (*KBAdd) Description() string {
	return "把新的资料写入知识库：可以是网页 URL，也可以是一段纯文本。用于用户要求「把这篇内容存下来」或在研究过程中沉淀重要结论。写入后立即可被 kb_search 检索到。"
}

// Schema implements [Tool].
func (*KBAdd) Schema() json.RawMessage {
	return SchemaFor([]string{"collection"}, map[string]string{
		"collection": `{"type":"string","description":"目标知识库名称或 id；不存在时会自动创建"}`,
		"url":        `{"type":"string","description":"要抓取并存入库的网页地址"}`,
		"text":       `{"type":"string","description":"要直接存入库的纯文本/Markdown 内容"}`,
		"title":      `{"type":"string","description":"文档标题"}`,
		"tags":       `{"type":"array","items":{"type":"string"},"description":"标签"}`,
	})
}

// Mutating implements [Tool].
func (*KBAdd) Mutating() bool { return true }

// Run implements [Tool].
func (*KBAdd) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	if ctx.KB == nil {
		return Result{}, fmt.Errorf("知识库未初始化")
	}
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	name := argString(args, "collection")
	if name == "" {
		return Result{}, fmt.Errorf("collection 不能为空")
	}

	collectionID, err := resolveCollection(ctx, name)
	if err != nil {
		return Result{}, err
	}

	title := argString(args, "title")
	tags := argStrings(args, "tags")

	switch {
	case argString(args, "url") != "":
		u := argString(args, "url")
		if err := ctx.Approve(ApprovalRequest{
			ToolName: "kb_add",
			Title:    "抓取网页并写入知识库",
			Detail:   fmt.Sprintf("目标知识库：%s\nURL：%s", name, u),
		}); err != nil {
			return Result{}, err
		}
		doc, err := ctx.KB.AddURL(ctx.Ctx, collectionID, u, kb.IngestOptions{Title: title, Replace: true, Tags: tags})
		if err != nil {
			return Result{}, err
		}
		return Result{
			Content: fmt.Sprintf("已写入《%s》，切分为 %d 个分块，可立即检索。", doc.Title, doc.ChunkCount),
			Display: fmt.Sprintf("已导入《%s》", doc.Title),
			Meta:    map[string]any{"documentId": doc.ID},
		}, nil

	case argString(args, "text") != "":
		body := argString(args, "text")
		if title == "" {
			title = firstRunes(strings.ReplaceAll(body, "\n", " "), 40)
		}
		if err := ctx.Approve(ApprovalRequest{
			ToolName: "kb_add",
			Title:    "写入文本到知识库",
			Detail:   fmt.Sprintf("目标知识库：%s\n标题：%s\n长度：%d 字符", name, title, len([]rune(body))),
		}); err != nil {
			return Result{}, err
		}
		source := "agent://" + title
		doc, err := ctx.KB.AddParsed(ctx.Ctx, collectionID,
			kb.ParseText(title, body), source, "text",
			kb.IngestOptions{Title: title, Tags: tags})
		if err != nil {
			return Result{}, err
		}
		return Result{
			Content: fmt.Sprintf("已写入《%s》，切分为 %d 个分块。", doc.Title, doc.ChunkCount),
			Display: fmt.Sprintf("已写入《%s》", doc.Title),
			Meta:    map[string]any{"documentId": doc.ID},
		}, nil
	}
	return Result{}, fmt.Errorf("需要提供 url 或 text 之一")
}

// resolveCollection maps a user-supplied name to an id, creating it if needed.
func resolveCollection(ctx *Context, name string) (string, error) {
	cols, err := ctx.KB.Collections()
	if err != nil {
		return "", err
	}
	for _, c := range cols {
		if c.ID == name || strings.EqualFold(c.Name, name) {
			return c.ID, nil
		}
	}
	// An exact-enough match on a distinctive name beats creating a near
	// duplicate, which is a common failure when the model abbreviates.
	for _, c := range cols {
		if containsFold(c.Name, name) || containsFold(name, c.Name) {
			return c.ID, nil
		}
	}
	created, err := ctx.KB.CreateCollection(name, "由 Agent 自动创建")
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// sortedKeys is a tiny helper for deterministic debug output.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
