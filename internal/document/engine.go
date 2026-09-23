// Package document owns file inspection, extraction and conversion. Knowledge
// storage consumes its output; it does not own the document parser lifecycle.
package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/skillruntime"
)

const maxExtractBytes = 96 << 10

type Engine struct{ runtime *skillruntime.Manager }

type Result struct {
	Title         string
	Text          string
	Mime          string
	Format        string
	Artifacts     []skillruntime.SkillArtifact
	Truncated     bool
	SkippedReason string
}

func NewEngine(runtime *skillruntime.Manager) *Engine { return &Engine{runtime: runtime} }

func (*Engine) Supported(path string) bool    { return Supported(path) }
func (*Engine) SupportedExtensions() []string { return SupportedExtensions() }

func Supported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pptx", ".xlsx", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp":
		return true
	default:
		return kb.Supported(path)
	}
}

func SupportedExtensions() []string {
	out := append([]string(nil), kb.SupportedExtensions...)
	out = append(out, ".pptx", ".xlsx", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp")
	return out
}

func (e *Engine) Inspect(ctx context.Context, path, workspace string) (Result, error) {
	abs, info, err := validateInput(path)
	if err != nil {
		return Result{}, err
	}
	result := Result{Title: strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)), Mime: mime.TypeByExtension(strings.ToLower(filepath.Ext(abs))), Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), ".")}
	if result.Mime == "" {
		result.Mime = "application/octet-stream"
	}
	result.Text = fmt.Sprintf("文件：%s\n格式：%s\n大小：%d 字节\n修改时间：%s", filepath.Base(abs), result.Format, info.Size(), info.ModTime().Format(time.RFC3339))
	return result, nil
}

func (e *Engine) Extract(ctx context.Context, path, workspace string) (Result, error) {
	abs, _, err := validateInput(path)
	if err != nil {
		return Result{}, err
	}
	if !needsDocling(abs) {
		parsed, parseErr := kb.ParseFile(abs)
		if parseErr != nil {
			return Result{}, parseErr
		}
		text, truncated := truncate(parsed.Text, maxExtractBytes)
		return Result{Title: parsed.Title, Text: text, Mime: parsed.Mime, Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), "."), Truncated: truncated, SkippedReason: parsed.SkippedReason}, nil
	}
	if e.runtime != nil {
		result, runErr := e.runtime.Execute(ctx, "document", skillruntime.SkillRequest{Command: "extract", Args: []string{"--path", abs}, Workspace: workspace})
		if runErr == nil {
			return fromSkill(result, abs), nil
		}
		// Existing PDF/DOCX parsing remains available when a development build
		// has not staged the bundled runtime yet.
		if ext := strings.ToLower(filepath.Ext(abs)); ext != ".pdf" && ext != ".docx" {
			return Result{}, runErr
		}
	}
	parsed, err := kb.ParseFile(abs)
	if err != nil {
		return Result{}, err
	}
	if parsed.SkippedReason != "" {
		return Result{}, errors.New(parsed.SkippedReason)
	}
	text, truncated := truncate(parsed.Text, maxExtractBytes)
	return Result{Title: parsed.Title, Text: text, Mime: parsed.Mime, Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), "."), Truncated: truncated}, nil
}

func (e *Engine) Open(ctx context.Context, path, workspace string, args []string) (Result, error) {
	abs, _, err := validateInput(path)
	if err != nil {
		return Result{}, err
	}
	if e.runtime != nil && needsDocling(abs) {
		requestArgs := append([]string{"--path", abs}, args...)
		result, runErr := e.runtime.Execute(ctx, "document", skillruntime.SkillRequest{Command: "open", Args: requestArgs, Workspace: workspace})
		if runErr == nil {
			return fromSkill(result, abs), nil
		}
	}
	return e.Extract(ctx, abs, workspace)
}

func (e *Engine) Convert(ctx context.Context, path, workspace string) (Result, error) {
	abs, _, err := validateInput(path)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(workspace) == "" {
		return Result{}, errors.New("转换文档需要工作区")
	}
	if e.runtime != nil && needsDocling(abs) {
		result, runErr := e.runtime.Execute(ctx, "document", skillruntime.SkillRequest{Command: "convert", Args: []string{"--path", abs}, Workspace: workspace})
		if runErr == nil {
			return fromSkill(result, abs), nil
		}
		if ext := strings.ToLower(filepath.Ext(abs)); ext != ".pdf" && ext != ".docx" {
			return Result{}, runErr
		}
	}
	extracted, err := e.Extract(ctx, abs, workspace)
	if err != nil {
		return Result{}, err
	}
	artifactDir := filepath.Join(workspace, ".hitk", "artifacts", "document_"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return Result{}, err
	}
	base := safeStem(abs)
	markdownPath := filepath.Join(artifactDir, base+".md")
	jsonPath := filepath.Join(artifactDir, base+".docling.json")
	if err := os.WriteFile(markdownPath, []byte(extracted.Text), 0o644); err != nil {
		return Result{}, err
	}
	payload, _ := json.MarshalIndent(map[string]any{"name": filepath.Base(abs), "mime": extracted.Mime, "text": extracted.Text, "fallback": true}, "", "  ")
	if err := os.WriteFile(jsonPath, payload, 0o644); err != nil {
		return Result{}, err
	}
	extracted.Artifacts = []skillruntime.SkillArtifact{
		{Path: markdownPath, Name: filepath.Base(markdownPath), Mime: "text/markdown", Operation: "created"},
		{Path: jsonPath, Name: filepath.Base(jsonPath), Mime: "application/json", Operation: "created"},
	}
	return extracted, nil
}

func validateInput(path string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return "", nil, errors.New("文档路径不能是目录")
	}
	if !Supported(abs) {
		return "", nil, fmt.Errorf("不支持的文档格式: %s", filepath.Ext(abs))
	}
	return abs, info, nil
}

func needsDocling(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf", ".docx", ".pptx", ".xlsx", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp":
		return true
	default:
		return false
	}
}
func fromSkill(result skillruntime.SkillExecutionResult, path string) Result {
	text, truncated := truncate(result.Content, maxExtractBytes)
	return Result{Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Text: text, Mime: mime.TypeByExtension(strings.ToLower(filepath.Ext(path))), Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."), Artifacts: result.Artifacts, Truncated: result.Truncated || truncated}
}
func truncate(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return value[:limit] + "\n\n[内容已截断，请使用 document open 分段读取]", true
}
func safeStem(path string) string {
	value := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	value = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x4e00 && r <= 0x9fff {
			return r
		}
		return '_'
	}, value)
	if value == "" {
		return "document"
	}
	return value
}
