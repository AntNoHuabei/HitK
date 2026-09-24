package skillcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

var pythonInstallHintRE = regexp.MustCompile(`(?i)(?:python(?:3)?\s+-m\s+)?pip(?:3)?\s+install\s+(?:--[^\s]+\s+)*([A-Za-z0-9_.@/+<>=\-]+)`)
var autoInstallRE = regexp.MustCompile(`_auto_install\(\s*["']([^"']+)["'](?:\s*,\s*["']([^"']+)["'])?\s*\)`)

type pythonScan struct {
	Imports []string                         `json:"imports"`
	Hints   []skillmodel.EmbeddedInstallHint `json:"hints"`
}

func analyzePython(ctx context.Context, root, pythonPath string) (pythonScan, error) {
	if pythonPath != "" {
		if _, err := os.Stat(pythonPath); err == nil {
			return runPythonAST(ctx, root, pythonPath)
		}
	}
	return scanPythonFallback(ctx, root)
}

func runPythonAST(ctx context.Context, root, pythonPath string) (pythonScan, error) {
	cmd := exec.CommandContext(ctx, pythonPath, "-I", "-B", "-c", pythonASTScript, root)
	cmd.Dir = root
	cmd.Env = pythonScannerEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return pythonScan{}, err
	}
	var result pythonScan
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return pythonScan{}, err
	}
	normalizePythonScan(&result)
	return result, nil
}

func scanPythonFallback(ctx context.Context, root string) (pythonScan, error) {
	var result pythonScan
	importRE := regexp.MustCompile(`(?m)^\s*(?:from\s+([A-Za-z_][\w.]*)\s+import|import\s+([A-Za-z_][\w.]*(?:\s*,\s*[A-Za-z_][\w.]*)*))`)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", "node_modules", ".venv", "venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".py" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		result.Hints = append(result.Hints, parseEmbeddedPythonHints(string(data))...)
		for _, match := range importRE.FindAllStringSubmatch(string(data), -1) {
			value := firstNonEmpty(match[1], match[2])
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if dot := strings.IndexByte(part, '.'); dot >= 0 {
					part = part[:dot]
				}
				if part != "" {
					if !isCommonPythonStdlib(part) {
						result.Imports = append(result.Imports, part)
					}
				}
			}
		}
		return nil
	})
	normalizePythonScan(&result)
	return result, err
}

func isCommonPythonStdlib(name string) bool {
	switch name {
	case "os", "sys", "re", "json", "math", "time", "datetime", "pathlib",
		"typing", "collections", "itertools", "functools", "subprocess",
		"argparse", "logging", "tempfile", "shutil", "io", "csv", "sqlite3",
		"zipfile", "urllib", "http", "socket", "threading", "asyncio",
		"unittest", "dataclasses", "enum", "contextlib", "hashlib", "random",
		"statistics", "base64", "mimetypes", "secrets", "traceback":
		return true
	default:
		return false
	}
}

func parseEmbeddedPythonHints(text string) []skillmodel.EmbeddedInstallHint {
	var hints []skillmodel.EmbeddedInstallHint
	for _, match := range pythonInstallHintRE.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			hints = append(hints, skillmodel.EmbeddedInstallHint{
				Ecosystem: "python",
				Command:   strings.TrimSpace(match[0]),
				Package:   strings.TrimSpace(match[1]),
			})
		}
	}
	for _, match := range autoInstallRE.FindAllStringSubmatch(text, -1) {
		pkg := firstNonEmpty(match[2], match[1])
		if pkg != "" {
			hints = append(hints, skillmodel.EmbeddedInstallHint{
				Ecosystem: "python",
				Command:   strings.TrimSpace(match[0]),
				Package:   pkg,
			})
		}
	}
	return hints
}

func normalizePythonScan(result *pythonScan) {
	result.Imports = uniqueSorted(result.Imports)
	hints := map[string]skillmodel.EmbeddedInstallHint{}
	for _, hint := range result.Hints {
		key := strings.ToLower(hint.Ecosystem + ":" + hint.Package)
		hints[key] = hint
	}
	result.Hints = result.Hints[:0]
	for _, hint := range hints {
		result.Hints = append(result.Hints, hint)
	}
	sort.Slice(result.Hints, func(i, j int) bool {
		if result.Hints[i].Ecosystem != result.Hints[j].Ecosystem {
			return result.Hints[i].Ecosystem < result.Hints[j].Ecosystem
		}
		return result.Hints[i].Package < result.Hints[j].Package
	})
}

func pythonScannerEnv() []string {
	env := []string{"PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1"}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if runtime.GOOS != "windows" {
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}

func uniqueSorted(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

const pythonASTScript = `
import ast, json, os, re, sys
root = sys.argv[1]
imports = set()
hints = []
local = set()
for current, dirs, files in os.walk(root):
    dirs[:] = [d for d in dirs if d not in {".git", "node_modules", ".venv", "venv", "__pycache__"}]
    for name in files:
        if name.endswith(".py"):
            local.add(os.path.splitext(name)[0])
            if name == "__init__.py":
                local.add(os.path.basename(current))
for current, dirs, files in os.walk(root):
    dirs[:] = [d for d in dirs if d not in {".git", "node_modules", ".venv", "venv", "__pycache__"}]
    for name in sorted(files):
        if not name.endswith(".py"):
            continue
        path = os.path.join(current, name)
        try:
            if os.path.getsize(path) > 2 * 1024 * 1024:
                continue
            text = open(path, "r", encoding="utf-8", errors="ignore").read()
            tree = ast.parse(text, filename=path)
        except Exception:
            continue
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    imports.add(alias.name.split(".")[0])
            elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
                imports.add(node.module.split(".")[0])
        for match in re.finditer(r"(?i)(?:python(?:3)?\s+-m\s+)?pip(?:3)?\s+install\s+(?:--[^\s]+\s+)*([A-Za-z0-9_.@/+<>=\-]+)", text):
            hints.append({"ecosystem": "python", "command": match.group(0), "package": match.group(1)})
        for match in re.finditer(r'_auto_install\(\s*["\']([^"\']+)["\'](?:\s*,\s*["\']([^"\']+)["\'])?\s*\)', text):
            hints.append({"ecosystem": "python", "command": match.group(0), "package": match.group(2) or match.group(1)})
stdlib = set(getattr(sys, "stdlib_module_names", set()))
imports.difference_update(stdlib)
imports.difference_update(local)
print(json.dumps({"imports": sorted(imports), "hints": hints}, ensure_ascii=False))
`
