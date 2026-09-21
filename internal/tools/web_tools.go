package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
)

// FetchURL retrieves a web page and converts it to readable text.
type FetchURL struct{}

// Name implements [Tool].
func (*FetchURL) Name() string { return "fetch_url" }

// DisplayName implements [Tool].
func (*FetchURL) DisplayName() string { return "抓取网页" }

// Description implements [Tool].
func (*FetchURL) Description() string {
	return "抓取一个网页或在线文本文件并转换为可读文本（HTML 会被剥离标签）。用于获取知识库之外的公开资料。注意：需要登录或由 JS 渲染的页面可能取不到正文。"
}

// Schema implements [Tool].
func (*FetchURL) Schema() json.RawMessage {
	return SchemaFor([]string{"url"}, map[string]string{
		"url": `{"type":"string","description":"完整 URL，必须以 http:// 或 https:// 开头"}`,
	})
}

// Mutating implements [Tool].
func (*FetchURL) Mutating() bool { return false }

// Run implements [Tool].
func (*FetchURL) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	target := argString(args, "url")
	if target == "" {
		return Result{}, fmt.Errorf("url 不能为空")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return Result{}, fmt.Errorf("url 必须以 http:// 或 https:// 开头")
	}

	reqCtx, cancel := context.WithTimeout(ctx.Ctx, 45*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HitK/0.1)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("抓取失败：HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Result{}, err
	}

	contentType := resp.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(contentType, "html") {
		text = kb.StripHTML(text)
	}

	title := target
	content, truncated := truncate(text, 48<<10)
	var b strings.Builder
	fmt.Fprintf(&b, "来源：%s\n\n%s", title, content)

	return Result{
		Content:   b.String(),
		Display:   shortHost(target),
		Truncated: truncated,
		Meta:      map[string]any{"url": target, "bytes": len(body)},
	}, nil
}

func shortHost(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i > 0 {
		s = s[:i]
	}
	return s
}

// TodoWrite maintains the agent's working checklist. Codex exposes the same
// idea as `update_plan`; keeping it as a tool (rather than internal state) means
// the model commits to a plan explicitly and the UI can render it live.
type TodoWrite struct{}

// Name implements [Tool].
func (*TodoWrite) Name() string { return "todo_write" }

// DisplayName implements [Tool].
func (*TodoWrite) DisplayName() string { return "更新任务清单" }

// Description implements [Tool].
func (*TodoWrite) Description() string {
	return "创建或更新当前任务的执行清单，让用户看到进度。多步骤任务（≥3 步）应先写清单再动手；每完成一步就调用一次把该项标为 completed。" +
		"传入的 todos 会整体替换旧清单，因此每次都要给出完整列表。"
}

// Schema implements [Tool].
func (*TodoWrite) Schema() json.RawMessage {
	return SchemaFor([]string{"todos"}, map[string]string{
		"todos": `{"type":"array","description":"完整任务清单","items":{"type":"object","properties":{"text":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["text","status"]}}`,
	})
}

// Mutating implements [Tool].
func (*TodoWrite) Mutating() bool { return false }

// Run implements [Tool].
func (*TodoWrite) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}

	items, _ := args["todos"].([]any)
	if len(items) == 0 {
		// An empty list is a legitimate way to clear the plan.
		return Result{Content: "任务清单已清空。", Display: "已清空", Plan: []protocol.TodoEntry{}}, nil
	}

	plan := make([]protocol.TodoEntry, 0, len(items))
	done, active := 0, 0
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		status, _ := m["status"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		switch status {
		case "completed":
			done++
		case "in_progress":
			active++
		default:
			status = "pending"
		}
		plan = append(plan, protocol.TodoEntry{Text: text, Status: status})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "当前清单（%d/%d 已完成）：\n", done, len(plan))
	for i, t := range plan {
		mark := "[ ]"
		switch t.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, mark, t.Text)
	}
	if active == 0 && done < len(plan) {
		b.WriteString("\n提示：请把正在执行的项标记为 in_progress。")
	}

	return Result{
		Content: b.String(),
		Display: fmt.Sprintf("清单 %d/%d", done, len(plan)),
		Plan:    plan,
	}, nil
}
