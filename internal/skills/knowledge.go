// Package skills contains model-facing, on-demand capabilities implemented as
// Cobra command trees. The command layer owns argument validation and output
// limits; callers only need to provide the execution context.
package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/spf13/cobra"
)

const (
	DefaultSearchLimit   = 6
	DefaultSnippetRunes  = 280
	MaxSearchLimit       = 12
	MaxSearchOutputBytes = 12 << 10
)

// Context is the environment inherited by a Skill invocation.
type Context struct {
	Ctx            context.Context
	KB             *kb.Store
	KnowledgeBases []string
	Approve        func(title, detail string) error
}

// Result is the model-facing result of a Skill command.
type Result struct {
	Content   string
	Display   string
	Refs      []protocol.Reference
	Artifacts []protocol.Artifact
	Truncated bool
	Meta      map[string]any
}

// RunKnowledge executes one validated knowledge subcommand.
func RunKnowledge(ctx *Context, command string, args []string) (Result, error) {
	if ctx == nil || ctx.KB == nil {
		return Result{}, errors.New("知识库未初始化")
	}
	if ctx.Ctx == nil {
		ctx.Ctx = context.Background()
	}

	root := newKnowledgeCommand(ctx)
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return Result{}, errors.New("skill_run 不支持帮助参数")
		}
	}
	root.SetArgs(append([]string{strings.ToLower(strings.TrimSpace(command))}, args...))
	root.SilenceErrors = true
	root.SilenceUsage = true
	var out commandOutput
	writer := &commandOutputWriter{output: &out}
	root.SetOut(writer)
	root.SetErr(writer)
	if err := root.ExecuteContext(ctx.Ctx); err != nil {
		return Result{}, err
	}
	if out.result == nil {
		return Result{}, errors.New("Skill 命令没有返回结果")
	}
	return *out.result, nil
}

type commandOutput struct {
	result *Result
}

func newKnowledgeCommand(ctx *Context) *cobra.Command {
	root := &cobra.Command{
		Use:           "knowledge",
		Short:         "按需检索和管理当前会话范围内的知识库",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(newCollectionsCommand(ctx), newSearchCommand(ctx), newOpenCommand(ctx), newAddCommand(ctx))
	return root
}

func newCollectionsCommand(ctx *Context) *cobra.Command {
	return &cobra.Command{
		Use:   "collections",
		Short: "列出可检索知识库的摘要",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cols, err := ctx.KB.Collections()
			if err != nil {
				return err
			}
			allowed, err := resolveScope(cols, ctx.KnowledgeBases)
			if err != nil {
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "知识库 %d 个\n", len(allowed))
			for _, c := range allowed {
				fmt.Fprintf(&b, "- %s | %d 文档 | %d 分块\n", c.Name, c.DocCount, c.ChunkCount)
			}
			return writeResult(cmd, Result{Content: b.String(), Display: fmt.Sprintf("%d 个知识库", len(allowed))})
		},
	}
}

func newSearchCommand(ctx *Context) *cobra.Command {
	var queries, collections []string
	var limit, snippet int
	cmd := &cobra.Command{
		Use:   "search",
		Short: "返回短证据和引用，不展开全文",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(queries) == 0 {
				return errors.New("search 需要至少一个 --query")
			}
			scope, err := effectiveScope(ctx, collections)
			if err != nil {
				return err
			}
			if limit <= 0 {
				limit = DefaultSearchLimit
			}
			if limit > MaxSearchLimit {
				limit = MaxSearchLimit
			}
			if snippet <= 0 {
				snippet = DefaultSnippetRunes
			}
			hits, err := ctx.KB.SearchExpanded(ctx.Ctx, queries, kb.SearchOptions{
				Collections: scope, Limit: limit, SnippetRunes: snippet, MinScore: 0.12,
			})
			if err != nil {
				return err
			}
			refs := make([]protocol.Reference, 0, len(hits))
			var b strings.Builder
			fmt.Fprintf(&b, "命中 %d 条短证据\n", len(hits))
			for i, hit := range hits {
				fmt.Fprintf(&b, "[%d] %s | %s | chunkId=%s | score=%.3f\n", i+1, hit.DocTitle, hit.Collection, hit.ChunkID, hit.Score)
				if hit.Heading != "" {
					fmt.Fprintf(&b, "小节：%s\n", hit.Heading)
				}
				fmt.Fprintf(&b, "%s\n\n", hit.Text)
				refs = append(refs, protocol.Reference{Collection: hit.Collection, DocID: hit.DocumentID, DocTitle: hit.DocTitle, ChunkID: hit.ChunkID, Path: hit.Source, Score: hit.Score, Snippet: clipRunes(hit.Text, 160)})
			}
			content, truncated := truncateBytes(b.String(), MaxSearchOutputBytes)
			if len(hits) == 0 {
				content = "未找到相关证据。不要把知识库内容当作已知事实；可以换检索词或明确告知用户资料不足。"
			}
			return writeResult(cmd, Result{Content: content, Display: fmt.Sprintf("命中 %d 条", len(hits)), Refs: refs, Truncated: truncated, Meta: map[string]any{"hits": len(hits)}})
		},
	}
	cmd.Flags().StringArrayVar(&queries, "query", nil, "检索式，可重复传入")
	cmd.Flags().StringArrayVar(&collections, "collection", nil, "限定知识库，可重复传入")
	cmd.Flags().IntVar(&limit, "limit", DefaultSearchLimit, "返回条数")
	cmd.Flags().IntVar(&snippet, "snippet", DefaultSnippetRunes, "每条证据的最大字数")
	return cmd
}

func newOpenCommand(ctx *Context) *cobra.Command {
	var chunkID, documentID string
	var window int
	cmd := &cobra.Command{
		Use:   "open",
		Short: "展开一个检索分块或查看文档目录",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if chunkID == "" && documentID == "" {
				return errors.New("open 需要 --chunk-id 或 --document-id")
			}
			if window < 0 {
				window = 0
			}
			if window > 3 {
				window = 3
			}
			if chunkID != "" {
				result, err := openChunk(ctx, chunkID, window)
				if err != nil {
					return err
				}
				return writeResult(cmd, result)
			}
			result, err := outline(ctx, documentID)
			if err != nil {
				return err
			}
			return writeResult(cmd, result)
		},
	}
	cmd.Flags().StringVar(&chunkID, "chunk-id", "", "检索结果中的 chunkId")
	cmd.Flags().StringVar(&documentID, "document-id", "", "文档 id")
	cmd.Flags().IntVar(&window, "window", 1, "前后展开分块数")
	return cmd
}

func newAddCommand(ctx *Context) *cobra.Command {
	var collection, url, text, title string
	var tags []string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "把 URL 或文本写入当前范围内的知识库",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if collection == "" || (url == "" && text == "") || (url != "" && text != "") {
				return errors.New("add 需要 --collection，以及 url 或 text 二选一")
			}
			resolved, err := effectiveScope(ctx, []string{collection})
			if err != nil {
				return err
			}
			collectionID := resolved[0]
			if ctx.Approve != nil {
				if err := ctx.Approve("写入知识库", fmt.Sprintf("目标知识库：%s", collection)); err != nil {
					return err
				}
			}
			var doc kb.Document
			var writeErr error
			if url != "" {
				doc, writeErr = ctx.KB.AddURL(ctx.Ctx, collectionID, url, kb.IngestOptions{Title: title, Replace: true, Tags: tags})
			} else {
				if title == "" {
					title = clipRunes(strings.ReplaceAll(text, "\n", " "), 40)
				}
				doc, writeErr = ctx.KB.AddParsed(ctx.Ctx, collectionID, kb.ParseText(title, text), "agent://"+title, "text", kb.IngestOptions{Title: title, Tags: tags})
			}
			if writeErr != nil {
				return writeErr
			}
			return writeResult(cmd, Result{Content: fmt.Sprintf("已写入《%s》，文档 id=%s，分块 %d，可立即检索。", doc.Title, doc.ID, doc.ChunkCount), Display: fmt.Sprintf("已写入《%s》", doc.Title), Meta: map[string]any{"documentId": doc.ID}})
		},
	}
	cmd.Flags().StringVar(&collection, "collection", "", "目标知识库 id 或名称")
	cmd.Flags().StringVar(&url, "url", "", "网页地址")
	cmd.Flags().StringVar(&text, "text", "", "纯文本或 Markdown")
	cmd.Flags().StringVar(&title, "title", "", "文档标题")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "标签")
	return cmd
}

func writeResult(cmd *cobra.Command, result Result) error {
	if output, ok := cmd.Root().OutOrStdout().(*commandOutputWriter); ok {
		output.output.result = &result
	}
	return nil
}

// commandOutputWriter bridges Cobra's io.Writer to the typed result without
// serializing a second model-facing envelope.
type commandOutputWriter struct{ output *commandOutput }

func (w *commandOutputWriter) Write(p []byte) (int, error) { return len(p), nil }

func effectiveScope(ctx *Context, requested []string) ([]string, error) {
	cols, err := ctx.KB.Collections()
	if err != nil {
		return nil, err
	}
	allowed, err := resolveScope(cols, ctx.KnowledgeBases)
	if err != nil {
		return nil, err
	}
	if len(requested) == 0 {
		out := make([]string, 0, len(allowed))
		for _, c := range allowed {
			out = append(out, c.ID)
		}
		return out, nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, c := range allowed {
		allowedSet[c.ID] = true
	}
	out := make([]string, 0, len(requested))
	for _, name := range requested {
		found := ""
		for _, c := range cols {
			if strings.EqualFold(c.ID, name) || strings.EqualFold(c.Name, name) {
				found = c.ID
			}
		}
		if found == "" || !allowedSet[found] {
			return nil, fmt.Errorf("知识库 %q 不在当前会话范围内", name)
		}
		out = append(out, found)
	}
	return out, nil
}

func resolveScope(cols []kb.Collection, scope []string) ([]kb.Collection, error) {
	if len(scope) == 0 {
		return cols, nil
	}
	set := make(map[string]bool, len(scope))
	for _, name := range scope {
		found := ""
		for _, c := range cols {
			if strings.EqualFold(c.ID, name) || strings.EqualFold(c.Name, name) {
				found = c.ID
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("知识库 %q 不存在", name)
		}
		set[found] = true
	}
	out := make([]kb.Collection, 0, len(set))
	for _, c := range cols {
		if set[c.ID] {
			out = append(out, c)
		}
	}
	return out, nil
}

func openChunk(ctx *Context, chunkID string, window int) (Result, error) {
	chunk, err := ctx.KB.Chunk(chunkID)
	if err != nil {
		return Result{}, err
	}
	if _, err := effectiveScope(ctx, []string{chunk.CollectionID}); err != nil {
		return Result{}, err
	}
	all, err := ctx.KB.Chunks(chunk.DocumentID)
	if err != nil {
		return Result{}, err
	}
	lo, hi := chunk.Ordinal-window, chunk.Ordinal+window+1
	if lo < 0 {
		lo = 0
	}
	if hi > len(all) {
		hi = len(all)
	}
	chunks := all[lo:hi]
	doc, err := ctx.KB.Document(chunk.DocumentID)
	if err != nil {
		return Result{}, err
	}
	collections, err := ctx.KB.Collections()
	if err != nil {
		return Result{}, err
	}
	collectionName := chunk.CollectionID
	for _, collection := range collections {
		if collection.ID == chunk.CollectionID {
			collectionName = collection.Name
			break
		}
	}
	var b strings.Builder
	for _, chunk := range chunks {
		fmt.Fprintf(&b, "[%s] %s\n%s\n\n", chunk.ID, chunk.Heading, chunk.Text)
	}
	content, truncated := truncateBytes(b.String(), 24<<10)
	ref := protocol.Reference{Collection: collectionName, DocID: doc.ID, DocTitle: doc.Title, ChunkID: chunk.ID, Path: doc.Source, Snippet: clipRunes(chunk.Text, 160)}
	return Result{Content: content, Display: fmt.Sprintf("展开 %d 个分块", len(chunks)), Refs: []protocol.Reference{ref}, Truncated: truncated}, nil
}

func outline(ctx *Context, documentID string) (Result, error) {
	doc, err := ctx.KB.Document(documentID)
	if err != nil {
		return Result{}, err
	}
	if _, err := effectiveScope(ctx, []string{doc.CollectionID}); err != nil {
		return Result{}, err
	}
	chunks, err := ctx.KB.Chunks(documentID)
	if err != nil {
		return Result{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "《%s》 | %s | %d 个分块\n", doc.Title, doc.Source, len(chunks))
	for _, chunk := range chunks {
		fmt.Fprintf(&b, "- #%d %s [%s]\n", chunk.Ordinal+1, clipRunes(chunk.Heading, 80), chunk.ID)
	}
	content, truncated := truncateBytes(b.String(), 12<<10)
	return Result{Content: content, Display: fmt.Sprintf("《%s》目录", doc.Title), Truncated: truncated}, nil
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func truncateBytes(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	end := limit - 80
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end] + fmt.Sprintf("\n… 已截断，原始 %d 字节 …\n", len(s)), true
}
