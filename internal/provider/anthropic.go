package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic implements the Anthropic Messages API.
type Anthropic struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

// AnthropicConfig configures the client.
type AnthropicConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// NewAnthropic builds an Anthropic client.
func NewAnthropic(cfg AnthropicConfig) *Anthropic {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	t := cfg.Timeout
	if t == 0 {
		t = 30 * time.Minute
	}
	return &Anthropic{baseURL: base, apiKey: cfg.APIKey, client: &http.Client{Timeout: t}}
}

// ID implements [Provider].
func (a *Anthropic) ID() string { return "anthropic" }

type antRequest struct {
	Model       string       `json:"model"`
	MaxTokens   int          `json:"max_tokens"`
	System      string       `json:"system,omitempty"`
	Messages    []antMessage `json:"messages"`
	Tools       []antTool    `json:"tools,omitempty"`
	ToolChoice  any          `json:"tool_choice,omitempty"`
	Stream      bool         `json:"stream"`
	Temperature *float64     `json:"temperature,omitempty"`
	Thinking    *antThinking `json:"thinking,omitempty"`
}

type antThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type antMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type antBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// image
	Source *antSource `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type antSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Stream implements [Provider].
func (a *Anthropic) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if a.apiKey == "" {
		return nil, ErrNoCredentials
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	body := antRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		System:      req.System,
		Messages:    toAntMessages(req.Messages),
		Stream:      true,
		Temperature: req.Temperature,
	}
	// Extended thinking is opt-in per request; when the caller asks for a
	// budget we forward it, otherwise Anthropic rejects the field on models
	// that do not support it.
	if budget := req.Metadata["thinking_budget"]; budget != "" {
		var n int
		fmt.Sscanf(budget, "%d", &n)
		if n >= 1024 {
			body.Thinking = &antThinking{Type: "enabled", BudgetTokens: n}
			// Anthropic requires temperature to be unset when thinking is on.
			body.Temperature = nil
			if maxTokens <= n {
				body.MaxTokens = n + 4096
			}
		}
	}
	for _, t := range req.Tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, antTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if req.ToolChoice != "" {
		body.ToolChoice = map[string]string{"type": "tool", "name": req.ToolChoice}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, summarizeError(msg))
	}

	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		a.pump(ctx, resp.Body, out)
	}()
	return out, nil
}

func (a *Anthropic) pump(ctx context.Context, r io.Reader, out chan<- Chunk) {
	// content_block_start opens a block; deltas for tool_use arrive as
	// partial JSON that must be concatenated before parsing.
	type blockState struct {
		kind string
		id   string
		name string
		json strings.Builder
	}
	blocks := map[int]*blockState{}

	send := func(ch Chunk) bool {
		select {
		case out <- ch:
			return true
		case <-ctx.Done():
			return false
		}
	}

	err := readSSE(ctx, r, func(payload []byte) bool {
		var frame struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
					CacheRead   int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock *struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			return true
		}
		if frame.Error != nil {
			send(Chunk{Kind: ChunkError, Err: fmt.Errorf("anthropic: %s", frame.Error.Message)})
			return false
		}

		switch frame.Type {
		case "message_start":
			if frame.Message != nil {
				send(Chunk{Kind: ChunkUsage, Usage: &Usage{
					InputTokens:  frame.Message.Usage.InputTokens,
					CachedTokens: frame.Message.Usage.CacheRead,
				}})
			}
		case "content_block_start":
			if cb := frame.ContentBlock; cb != nil {
				st := &blockState{kind: cb.Type, id: cb.ID, name: cb.Name}
				// Non-streaming vendors (and some proxies) deliver the whole
				// tool input in the start block instead of deltas.
				if len(cb.Input) > 0 && string(cb.Input) != "{}" {
					st.json.Write(cb.Input)
				}
				blocks[frame.Index] = st
				if cb.Type == "text" && cb.Text != "" {
					send(Chunk{Kind: ChunkText, Text: cb.Text})
				}
			}
		case "content_block_delta":
			st := blocks[frame.Index]
			if st == nil || frame.Delta == nil {
				return true
			}
			switch {
			case frame.Delta.Text != "":
				if !send(Chunk{Kind: ChunkText, Text: frame.Delta.Text}) {
					return false
				}
			case frame.Delta.Thinking != "":
				if !send(Chunk{Kind: ChunkReasoning, Text: frame.Delta.Thinking}) {
					return false
				}
			case frame.Delta.PartialJSON != "":
				st.json.WriteString(frame.Delta.PartialJSON)
			}
		case "content_block_stop":
			st := blocks[frame.Index]
			if st == nil || st.kind != "tool_use" || st.name == "" {
				return true
			}
			args := strings.TrimSpace(st.json.String())
			if args == "" {
				args = "{}"
			}
			if !send(Chunk{Kind: ChunkToolCall, Tool: &ToolCallReq{ID: st.id, Name: st.name, Arguments: args}}) {
				return false
			}
			delete(blocks, frame.Index)
		case "message_delta":
			if frame.Usage != nil {
				send(Chunk{Kind: ChunkUsage, Usage: &Usage{OutputTokens: frame.Usage.OutputTokens}})
			}
		case "message_stop":
			return false
		}
		return true
	})
	if err != nil && ctx.Err() == nil {
		send(Chunk{Kind: ChunkError, Err: fmt.Errorf("anthropic: %w", err)})
		return
	}
	send(Chunk{Kind: ChunkDone})
}

// toAntMessages converts HitK messages to Anthropic's block form. Anthropic
// has no `system` or `tool` roles, so system text is hoisted and tool results
// are folded into the following user turn.
func toAntMessages(msgs []Message) []antMessage {
	var out []antMessage
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			continue // hoisted into antRequest.System
		case RoleTool:
			block := antBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Text()}
			// Merge into a trailing user message when possible — Anthropic
			// requires tool_result blocks to sit in a user turn.
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				if blocks, ok := out[n-1].Content.([]antBlock); ok {
					out[n-1].Content = append(blocks, block)
					continue
				}
			}
			out = append(out, antMessage{Role: "user", Content: []antBlock{block}})
		case RoleAssistant:
			blocks := make([]antBlock, 0, len(m.Parts)+len(m.ToolCalls))
			for _, p := range m.Parts {
				if p.Type == "text" && p.Text != "" {
					blocks = append(blocks, antBlock{Type: "text", Text: p.Text})
				}
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(input) == 0 || !json.Valid(input) {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, antBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, antMessage{Role: "assistant", Content: blocks})
		default: // user
			blocks := make([]antBlock, 0, len(m.Parts))
			for _, p := range m.Parts {
				if p.Type == "image" && p.Data != "" {
					blocks = append(blocks, antBlock{
						Type:   "image",
						Source: &antSource{Type: "base64", MediaType: p.MimeType, Data: p.Data},
					})
					continue
				}
				blocks = append(blocks, antBlock{Type: "text", Text: p.Text})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, antBlock{Type: "text", Text: ""})
			}
			out = append(out, antMessage{Role: "user", Content: blocks})
		}
	}
	return out
}
