package tools

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxFileBytes caps how much of a single file is read into the conversation.
const maxFileBytes = 2 << 20

// ReadFile returns file contents with line numbers, so the model can cite and
// patch exact regions.
type ReadFile struct{}

// Name implements [Tool].
func (*ReadFile) Name() string { return "read_file" }

// DisplayName implements [Tool].
func (*ReadFile) DisplayName() string { return "读取文件" }

// Description implements [Tool].
func (*ReadFile) Description() string {
	return "读取文本文件内容，返回带行号的文本。支持通过 offset/limit 分段读取大文件；超过 2MB 的文件会被截断。修改文件前应先读取以确认当前内容。"
}

// Schema implements [Tool].
func (*ReadFile) Schema() json.RawMessage {
	return SchemaFor([]string{"path"}, map[string]string{
		"path":   `{"type":"string","description":"文件路径（相对工作区或绝对路径）"}`,
		"offset": `{"type":"integer","description":"起始行号，从 1 开始"}`,
		"limit":  `{"type":"integer","description":"最多读取的行数，默认 2000"}`,
	})
}

// Mutating implements [Tool].
func (*ReadFile) Mutating() bool { return false }

// Run implements [Tool].
func (*ReadFile) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	path := argString(args, "path")
	if path == "" {
		return Result{}, fmt.Errorf("path 不能为空")
	}
	abs, err := resolvePath(ctx, path)
	if err != nil {
		return Result{}, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return Result{}, err
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("%s 是目录，请使用 list_dir", path)
	}
	if info.Size() > maxFileBytes {
		return Result{}, fmt.Errorf("文件过大（%.1f MB），请使用 grep_search 定位后再读取片段",
			float64(info.Size())/(1<<20))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	offset := argInt(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := argInt(args, "limit", 2000)
	if limit <= 0 {
		limit = 2000
	}
	start := offset - 1
	if start >= len(lines) {
		return Result{
			Content: fmt.Sprintf("offset %d 超出文件范围（共 %d 行）", offset, len(lines)),
			Display: "超出范围",
		}, nil
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s（共 %d 行）\n", displayPath(ctx, abs), len(lines))
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%6d│ %s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n… 还有 %d 行，可用 offset=%d 继续读取\n", len(lines)-end, end+1)
	}

	content, truncated := truncate(b.String(), 64<<10)
	return Result{
		Content:   content,
		Display:   fmt.Sprintf("%s (%d 行)", displayPath(ctx, abs), end-start),
		Truncated: truncated,
	}, nil
}

// WriteFile creates or overwrites a file.
type WriteFile struct{}

// Name implements [Tool].
func (*WriteFile) Name() string { return "write_file" }

// DisplayName implements [Tool].
func (*WriteFile) DisplayName() string { return "写入文件" }

// Description implements [Tool].
func (*WriteFile) Description() string {
	return "创建新文件或整体覆盖已有文件。会覆盖全部内容，修改已有文件请优先使用 edit_file 做局部替换。写入前会请求用户确认。"
}

// Schema implements [Tool].
func (*WriteFile) Schema() json.RawMessage {
	return SchemaFor([]string{"path", "content"}, map[string]string{
		"path":    `{"type":"string","description":"文件路径"}`,
		"content": `{"type":"string","description":"完整文件内容"}`,
	})
}

// Mutating implements [Tool].
func (*WriteFile) Mutating() bool { return true }

// Run implements [Tool].
func (*WriteFile) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	path := argString(args, "path")
	if path == "" {
		return Result{}, fmt.Errorf("path 不能为空")
	}
	content, _ := args["content"].(string)
	abs, err := resolvePath(ctx, path)
	if err != nil {
		return Result{}, err
	}

	action := "创建"
	created := true
	if prev, err := os.ReadFile(abs); err == nil {
		if string(prev) == content {
			return Result{Content: "文件内容未变化，跳过写入。", Display: "无变化"}, nil
		}
		action = "覆盖"
		created = false
	}

	if err := ctx.Approve(ApprovalRequest{
		ToolName: "write_file",
		Title:    action + "文件",
		Detail:   fmt.Sprintf("%s（%d 字符）", displayPath(ctx, abs), len([]rune(content))),
		Diff:     previewDiff(abs, content),
	}); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	lines := strings.Count(content, "\n") + 1
	return Result{
		Content: fmt.Sprintf("已%s %s（%d 行）", action, displayPath(ctx, abs), lines),
		Display: fmt.Sprintf("%s %s", action, filepath.Base(abs)),
		Meta:    map[string]any{"path": abs, "lines": lines, "created": created},
	}, nil
}

// EditFile performs an exact string replacement.
type EditFile struct{}

// Name implements [Tool].
func (*EditFile) Name() string { return "edit_file" }

// DisplayName implements [Tool].
func (*EditFile) DisplayName() string { return "编辑文件" }

// Description implements [Tool].
func (*EditFile) Description() string {
	return "在文件中把 old_string 精确替换为 new_string。old_string 必须在文件中唯一出现，否则需要给出更多上下文行；设置 replace_all 可替换全部出现。这是修改代码的首选方式。"
}

// Schema implements [Tool].
func (*EditFile) Schema() json.RawMessage {
	return SchemaFor([]string{"path", "old_string", "new_string"}, map[string]string{
		"path":        `{"type":"string","description":"文件路径"}`,
		"old_string":  `{"type":"string","description":"要被替换的原始文本（须唯一匹配）"}`,
		"new_string":  `{"type":"string","description":"替换后的文本，留空表示删除"}`,
		"replace_all": `{"type":"boolean","description":"是否替换所有匹配项，默认 false"}`,
	})
}

// Mutating implements [Tool].
func (*EditFile) Mutating() bool { return true }

// Run implements [Tool].
func (*EditFile) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	path := argString(args, "path")
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if path == "" {
		return Result{}, fmt.Errorf("path 不能为空")
	}
	if oldStr == "" {
		return Result{}, fmt.Errorf("old_string 不能为空；如需整体覆盖请使用 write_file")
	}
	if oldStr == newStr {
		return Result{Content: "old_string 与 new_string 相同，无需修改。", Display: "无变化"}, nil
	}

	abs, err := resolvePath(ctx, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	original := string(data)

	count := strings.Count(original, oldStr)
	if count == 0 {
		return Result{}, fmt.Errorf("在 %s 中未找到 old_string。请先用 read_file 确认原文（注意缩进与换行）",
			displayPath(ctx, abs))
	}
	replaceAll := argBool(args, "replace_all", false)
	if count > 1 && !replaceAll {
		return Result{}, fmt.Errorf("old_string 在文件中出现 %d 次，不唯一。请补充上下文，或设置 replace_all=true", count)
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(original, oldStr, newStr)
	} else {
		updated = strings.Replace(original, oldStr, newStr, 1)
	}

	if err := ctx.Approve(ApprovalRequest{
		ToolName: "edit_file",
		Title:    "编辑文件",
		Detail:   fmt.Sprintf("%s（%d 处替换）", displayPath(ctx, abs), count),
		Diff:     makeDiff(oldStr, newStr),
	}); err != nil {
		return Result{}, err
	}

	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Content: fmt.Sprintf("已修改 %s，替换 %d 处。", displayPath(ctx, abs), count),
		Display: fmt.Sprintf("修改 %s", filepath.Base(abs)),
		Meta:    map[string]any{"path": abs, "replacements": count},
	}, nil
}

// ListDir lists a directory tree.
type ListDir struct{}

// Name implements [Tool].
func (*ListDir) Name() string { return "list_dir" }

// DisplayName implements [Tool].
func (*ListDir) DisplayName() string { return "浏览目录" }

// Description implements [Tool].
func (*ListDir) Description() string {
	return "以树状列出目录内容，用于快速了解项目结构。默认深度 2，会自动跳过 .git、node_modules 等目录。"
}

// Schema implements [Tool].
func (*ListDir) Schema() json.RawMessage {
	return SchemaFor(nil, map[string]string{
		"path":  `{"type":"string","description":"目录路径，默认为工作区根目录"}`,
		"depth": `{"type":"integer","description":"递归深度，默认 2，最大 5"}`,
	})
}

// Mutating implements [Tool].
func (*ListDir) Mutating() bool { return false }

// Run implements [Tool].
func (*ListDir) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	target := argString(args, "path")
	if target == "" {
		target = "."
	}
	abs, err := resolvePath(ctx, target)
	if err != nil {
		return Result{}, err
	}
	depth := argInt(args, "depth", 2)
	if depth < 1 {
		depth = 1
	}
	if depth > 5 {
		depth = 5
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", displayPath(ctx, abs))
	entries := 0
	const maxEntries = 600

	var walk func(dir string, level int, prefix string)
	walk = func(dir string, level int, prefix string) {
		if level > depth || entries > maxEntries {
			return
		}
		kids, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(kids, func(i, j int) bool {
			// Directories first, then alphabetical — matches how editors show
			// a tree and makes the output cheaper to skim.
			if kids[i].IsDir() != kids[j].IsDir() {
				return kids[i].IsDir()
			}
			return kids[i].Name() < kids[j].Name()
		})
		for _, kid := range kids {
			if entries > maxEntries {
				return
			}
			name := kid.Name()
			if kid.IsDir() && skipDirs[name] {
				continue
			}
			if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
				continue
			}
			entries++
			if kid.IsDir() {
				fmt.Fprintf(&b, "%s%s/\n", prefix, name)
				walk(filepath.Join(dir, name), level+1, prefix+"  ")
				continue
			}
			size := int64(0)
			if info, err := kid.Info(); err == nil {
				size = info.Size()
			}
			fmt.Fprintf(&b, "%s%s (%s)\n", prefix, name, humanSize(size))
		}
	}
	walk(abs, 1, "")

	if entries > maxEntries {
		fmt.Fprintf(&b, "\n… 目录过大，仅显示前 %d 项\n", maxEntries)
	}
	content, truncated := truncate(b.String(), 32<<10)
	return Result{
		Content:   content,
		Display:   fmt.Sprintf("%s（%d 项）", displayPath(ctx, abs), entries),
		Truncated: truncated,
	}, nil
}

// GlobFind matches files by pattern.
type GlobFind struct{}

// Name implements [Tool].
func (*GlobFind) Name() string { return "glob_find" }

// DisplayName implements [Tool].
func (*GlobFind) DisplayName() string { return "按名称查找" }

// Description implements [Tool].
func (*GlobFind) Description() string {
	return "按 glob 模式查找文件路径，例如 **/*.go、src/**/*.tsx。适合按文件名或扩展名定位，比 grep_search 更快。"
}

// Schema implements [Tool].
func (*GlobFind) Schema() json.RawMessage {
	return SchemaFor([]string{"pattern"}, map[string]string{
		"pattern": `{"type":"string","description":"glob 模式，如 **/*.go"}`,
		"path":    `{"type":"string","description":"搜索根目录，默认工作区"}`,
	})
}

// Mutating implements [Tool].
func (*GlobFind) Mutating() bool { return false }

// Run implements [Tool].
func (*GlobFind) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	pattern := argString(args, "pattern")
	if pattern == "" {
		return Result{}, fmt.Errorf("pattern 不能为空")
	}
	root := argString(args, "path")
	if root == "" {
		root = "."
	}
	absRoot, err := resolvePath(ctx, root)
	if err != nil {
		return Result{}, err
	}

	const maxResults = 300
	var matches []string
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= maxResults {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(absRoot, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if ok, _ := filepath.Match(pattern, rel); ok {
			matches = append(matches, rel)
			return nil
		}
		// `**/` is not supported by filepath.Match, so handle it explicitly by
		// matching the pattern against the basename as a fallback.
		if strings.HasPrefix(pattern, "**/") {
			if ok, _ := filepath.Match(pattern[3:], filepath.Base(rel)); ok {
				matches = append(matches, rel)
			}
		}
		return nil
	})

	if len(matches) == 0 {
		return Result{Content: fmt.Sprintf("没有匹配 %s 的文件。", pattern), Display: "0 个文件"}, nil
	}
	sort.Strings(matches)
	content, truncated := truncate(strings.Join(matches, "\n"), 16<<10)
	return Result{
		Content:   fmt.Sprintf("匹配 %s 的文件（%d 个）：\n%s", pattern, len(matches), content),
		Display:   fmt.Sprintf("%d 个文件", len(matches)),
		Truncated: truncated,
	}, nil
}

// GrepSearch searches file contents with a regular expression.
type GrepSearch struct{}

// Name implements [Tool].
func (*GrepSearch) Name() string { return "grep_search" }

// DisplayName implements [Tool].
func (*GrepSearch) DisplayName() string { return "全文搜索" }

// Description implements [Tool].
func (*GrepSearch) Description() string {
	return "用正则表达式搜索文件内容，返回「文件:行号: 内容」。用于定位符号定义、调用点或任意文本。可用 include 限定文件类型（如 *.go）。"
}

// Schema implements [Tool].
func (*GrepSearch) Schema() json.RawMessage {
	return SchemaFor([]string{"pattern"}, map[string]string{
		"pattern": `{"type":"string","description":"正则表达式"}`,
		"path":    `{"type":"string","description":"搜索根目录，默认工作区"}`,
		"include": `{"type":"string","description":"文件名过滤 glob，如 *.go、*_test.py"}`,
	})
}

// Mutating implements [Tool].
func (*GrepSearch) Mutating() bool { return false }

// Run implements [Tool].
func (*GrepSearch) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	pattern := argString(args, "pattern")
	if pattern == "" {
		return Result{}, fmt.Errorf("pattern 不能为空")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, fmt.Errorf("正则表达式无效: %w", err)
	}
	root := argString(args, "path")
	if root == "" {
		root = "."
	}
	absRoot, err := resolvePath(ctx, root)
	if err != nil {
		return Result{}, err
	}
	include := argString(args, "include")

	const (
		maxMatches = 200
		maxLineLen = 400
	)
	var (
		out     strings.Builder
		matches int
		scanned int
	)
	_ = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if matches >= maxMatches {
			return filepath.SkipAll
		}
		if include != "" {
			if ok, _ := filepath.Match(include, d.Name()); !ok {
				return nil
			}
		}
		scanned++
		// Binary files defeat line-based matching and waste I/O; the extension
		// check is cheaper than reading and probing the header.
		if !isProbablyText(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(absRoot, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if !re.MatchString(line) {
				continue
			}
			matches++
			shown := line
			if len(shown) > maxLineLen {
				shown = shown[:maxLineLen] + "…"
			}
			fmt.Fprintf(&out, "%s:%d: %s\n", rel, i+1, strings.TrimSpace(shown))
			if matches >= maxMatches {
				break
			}
		}
		return nil
	})

	if matches == 0 {
		return Result{
			Content: fmt.Sprintf("在 %d 个文件中没有找到匹配 %s 的内容。", scanned, pattern),
			Display: "无匹配",
		}, nil
	}
	content, truncated := truncate(out.String(), 32<<10)
	return Result{
		Content:   fmt.Sprintf("匹配 %s（%d 处）：\n%s", pattern, matches, content),
		Display:   fmt.Sprintf("%d 处匹配", matches),
		Truncated: truncated,
	}, nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// skipDirs are directory names never worth walking. Vendored and generated
// trees dominate file counts and never contain the answer.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, ".next": true, ".nuxt": true, "target": true,
	"__pycache__": true, ".venv": true, "venv": true, ".idea": true,
	".vscode": true, "coverage": true, ".cache": true, ".tox": true,
	"Pods": true, "DerivedData": true, "bin": true, "obj": true,
}

// resolvePath turns a user-supplied path into an absolute path, anchoring
// relative paths at the workspace root.
func resolvePath(ctx *Context, p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(p, `"`))
	if p == "" {
		return "", fmt.Errorf("路径为空")
	}
	// Accept file:// URLs and Windows paths that arrive with forward slashes.
	p = strings.TrimPrefix(p, "file://")
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	base := ctx.Workspace
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = wd
	}
	return filepath.Clean(filepath.Join(base, p)), nil
}

// displayPath shortens absolute paths under the workspace to relative ones so
// tool output stays readable and portable.
func displayPath(ctx *Context, abs string) string {
	if ctx.Workspace != "" {
		if rel, err := filepath.Rel(ctx.Workspace, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return abs
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

// isProbablyText filters obvious binaries by extension.
func isProbablyText(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".svgz",
		".pdf", ".zip", ".gz", ".tar", ".7z", ".rar", ".exe", ".dll", ".so",
		".dylib", ".bin", ".wasm", ".woff", ".woff2", ".ttf", ".otf",
		".mp3", ".mp4", ".mov", ".avi", ".wav", ".class", ".jar", ".o", ".a":
		return false
	}
	return true
}

// previewDiff builds a before/after preview for an overwrite approval.
func previewDiff(abs, content string) string {
	prev, err := os.ReadFile(abs)
	if err != nil {
		return "新建文件，共 " + humanSize(int64(len(content)))
	}
	return makeDiff(firstRunes(string(prev), 1200), firstRunes(content, 1200))
}

func firstRunes(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}

// makeDiff renders a compact unified-ish diff of two strings.
func makeDiff(oldStr, newStr string) string {
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	var b strings.Builder
	for _, l := range oldLines {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	for _, l := range newLines {
		fmt.Fprintf(&b, "+ %s\n", l)
	}
	return b.String()
}
