package skillcatalog

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

var (
	nodeFromRE        = regexp.MustCompile(`(?m)\bfrom\s+["']([^"']+)["']`)
	nodeImportRE      = regexp.MustCompile(`(?m)\bimport\s*\(\s*["']([^"']+)["']\s*\)|\brequire\s*\(\s*["']([^"']+)["']\s*\)`)
	nodeInstallHintRE = regexp.MustCompile(`(?i)npm\s+install\s+(?:--[^\s]+\s+)*(@?[A-Za-z0-9._/@\-]+)`)
)

func analyzeNode(ctx context.Context, root string) ([]string, []skillmodel.EmbeddedInstallHint, error) {
	imports := map[string]struct{}{}
	hints := map[string]skillmodel.EmbeddedInstallHint{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", "node_modules", ".venv", "venv", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx":
		default:
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.Size() > 2<<20 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := stripJavaScriptCommentsAndStrings(string(data))
		for _, match := range nodeFromRE.FindAllStringSubmatch(text, -1) {
			addNodeImport(imports, match[1])
		}
		for _, match := range nodeImportRE.FindAllStringSubmatch(text, -1) {
			addNodeImport(imports, firstNonEmpty(match[1], match[2]))
		}
		for _, match := range nodeInstallHintRE.FindAllStringSubmatch(string(data), -1) {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				pkg := strings.TrimSpace(match[1])
				hints[pkg] = skillmodel.EmbeddedInstallHint{Ecosystem: "node", Command: strings.TrimSpace(match[0]), Package: pkg}
			}
		}
		return nil
	})
	out := make([]string, 0, len(imports))
	for value := range imports {
		out = append(out, value)
	}
	sort.Strings(out)
	hintList := make([]skillmodel.EmbeddedInstallHint, 0, len(hints))
	for _, hint := range hints {
		hintList = append(hintList, hint)
	}
	sort.Slice(hintList, func(i, j int) bool { return hintList[i].Package < hintList[j].Package })
	return out, hintList, err
}

func addNodeImport(imports map[string]struct{}, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "node:") {
		return
	}
	parts := strings.Split(raw, "/")
	if strings.HasPrefix(raw, "@") && len(parts) >= 2 {
		imports[parts[0]+"/"+parts[1]] = struct{}{}
		return
	}
	imports[parts[0]] = struct{}{}
}

// stripJavaScriptCommentsAndStrings removes comments but deliberately preserves
// import and require string literals. The analyzer only generates candidates;
// every package is still validated by the package manager before use.
func stripJavaScriptCommentsAndStrings(text string) string {
	var b strings.Builder
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(text); i++ {
		if inLineComment {
			if text[i] == '\n' {
				inLineComment = false
				b.WriteByte('\n')
			}
			continue
		}
		if inBlockComment {
			if i+1 < len(text) && text[i] == '*' && text[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if i+1 < len(text) && text[i] == '/' && text[i+1] == '/' {
			inLineComment = true
			i++
			continue
		}
		if i+1 < len(text) && text[i] == '/' && text[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}
