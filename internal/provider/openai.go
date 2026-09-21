package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompat implements [Provider] against any vendor that serves the OpenAI
// `/chat/completions` API. HitK routes 14 of its 17 preset vendors through this
// one client; only Anthropic and Gemini need bespoke code, and even they share
// this file's SSE reading helpers.
type OpenAICompat struct {
	id        string
	label     string
	baseURL   string
	apiKey    string
	headers   map[string]string
	client    *http.Client
	embedding string
}

// OpenAICompatConfig configures a client.
type OpenAICompatConfig struct {
	ID        string
	Label     string
	BaseURL   string
	APIKey    string
	Headers   map[string]string
	Embedding string
	Timeout   time.Duration
}

// NewOpenAICompat builds an OpenAI-compatible client.
func NewOpenAICompat(cfg OpenAICompatConfig) *OpenAICompat {
	timeout := cfg.Timeout
	if timeout == 0 {
		// Streaming responses can legitimately idle while the model thinks, so
		// the overall timeout is generous; per-read stalls are caught by the
		// request context instead.
		timeout = 30 * time.Minute
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAICompat{
		id:        cfg.ID,
		label:     cfg.Label,
		baseURL:   base,
		apiKey:    cfg.APIKey,
		headers:   cfg.Headers,
		client:    &http.Client{Timeout: timeout},
		embedding: cfg.Embedding,
	}
}

// ID implements [Provider].
func (c *OpenAICompat) ID() string { return c.id }

// BaseURL returns the configured endpoint.
func (c *OpenAICompat) BaseURL() string { return c.baseURL }

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type oaiRequest struct {
	Model           string            `json:"model"`
	Messages        []oaiMessage      `json:"messages"`
	Tools           []oaiTool         `json:"tools,omitempty"`
	ToolChoice      string            `json:"tool_choice,omitempty"`
	Stream          bool              `json:"stream"`
	StreamOptions   *oaiStreamOptions `json:"stream_options,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Thinking        *oaiThinking      `json:"thinking,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiThinking struct {
	Type string `json:"type"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
	// DeepSeek's reasoner requires the reasoning content to be echoed back on
	// follow-up turns; unknown fields are ignored by every other vendor.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oaiToolCall struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function oaiToolCallInner `json:"function"`
}

type oaiToolCallInner struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiToolFunc `json:"function"`
}

type oaiToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaiStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string        `json:"role"`
			Content          *string       `json:"content"`
			ReasoningContent *string       `json:"reasoning_content"`
			Reasoning        *string       `json:"reasoning"`
			ToolCalls        []oaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		PromptDetails    *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

// Stream implements [Provider]. It performs the HTTP request synchronously (so
// authentication failures surface as a returned error the caller can show
// immediately) and then pumps SSE frames into the returned channel.
func (c *OpenAICompat) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("provider %s: base URL not configured", c.id)
	}
	if c.apiKey == "" && !c.local() {
		return nil, ErrNoCredentials
	}

	body := oaiRequest{
		Model:           req.Model,
		Messages:        toOAIMessages(req.System, req.Messages),
		Stream:          true,
		StreamOptions:   &oaiStreamOptions{IncludeUsage: true},
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: req.Metadata["reasoning_effort"],
	}
	if thinkingType := req.Metadata["thinking_type"]; thinkingType != "" {
		body.Thinking = &oaiThinking{Type: thinkingType}
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, oaiTool{
			Type:     "function",
			Function: oaiToolFunc{Name: t.Name, Description: t.Description, Parameters: params},
		})
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("%s: HTTP %d: %s", c.id, resp.StatusCode, summarizeError(msg))
	}

	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		c.pump(ctx, resp.Body, out)
	}()
	return out, nil
}

func (c *OpenAICompat) pump(ctx context.Context, r io.Reader, out chan<- Chunk) {
	// Tool-call deltas arrive split across frames and must be stitched back
	// together by index before they can be executed.
	type pending struct {
		id   string
		name strings.Builder
		args strings.Builder
	}
	toolCalls := map[int]*pending{}
	var order []int

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)

	send := func(ch Chunk) bool {
		select {
		case out <- ch:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var frame oaiStreamChunk
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			// A single malformed frame should not kill an otherwise healthy
			// stream; vendors occasionally emit keep-alive junk.
			continue
		}
		if frame.Error != nil {
			send(Chunk{Kind: ChunkError, Err: fmt.Errorf("%s: %s", c.id, frame.Error.Message)})
			return
		}
		if frame.Usage != nil {
			u := &Usage{
				InputTokens:  frame.Usage.PromptTokens,
				OutputTokens: frame.Usage.CompletionTokens,
				TotalTokens:  frame.Usage.TotalTokens,
			}
			if frame.Usage.PromptDetails != nil {
				u.CachedTokens = frame.Usage.PromptDetails.CachedTokens
			}
			if u.TotalTokens == 0 {
				u.TotalTokens = u.InputTokens + u.OutputTokens
			}
			if !send(Chunk{Kind: ChunkUsage, Usage: u}) {
				return
			}
		}

		for _, ch := range frame.Choices {
			d := ch.Delta
			if d.Content != nil && *d.Content != "" {
				if !send(Chunk{Kind: ChunkText, Text: *d.Content}) {
					return
				}
			}
			// Vendors disagree on the field name for reasoning deltas.
			for _, rp := range []*string{d.ReasoningContent, d.Reasoning} {
				if rp != nil && *rp != "" {
					if !send(Chunk{Kind: ChunkReasoning, Text: *rp}) {
						return
					}
				}
			}
			for i, tc := range d.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				p := toolCalls[idx]
				if p == nil {
					p = &pending{}
					toolCalls[idx] = p
					order = append(order, idx)
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				p.name.WriteString(tc.Function.Name)
				p.args.WriteString(tc.Function.Arguments)
			}
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		send(Chunk{Kind: ChunkError, Err: fmt.Errorf("%s: stream read: %w", c.id, err)})
		return
	}

	for _, idx := range order {
		p := toolCalls[idx]
		name := strings.TrimSpace(p.name.String())
		if name == "" {
			continue
		}
		args := strings.TrimSpace(p.args.String())
		if args == "" {
			args = "{}"
		}
		if !send(Chunk{Kind: ChunkToolCall, Tool: &ToolCallReq{ID: p.id, Name: name, Arguments: args}}) {
			return
		}
	}
	send(Chunk{Kind: ChunkDone})
}

// local reports whether the endpoint needs no API key.
func (c *OpenAICompat) local() bool {
	return strings.Contains(c.baseURL, "127.0.0.1") || strings.Contains(c.baseURL, "localhost")
}

// toOAIMessages flattens HitK messages into the OpenAI wire shape.
func toOAIMessages(system string, msgs []Message) []oaiMessage {
	out := make([]oaiMessage, 0, len(msgs)+1)
	if strings.TrimSpace(system) != "" {
		out = append(out, oaiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		om := oaiMessage{Role: string(m.Role), ToolCallID: m.ToolCallID, Name: m.Name}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, oaiToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: oaiToolCallInner{Name: tc.Name, Arguments: tc.Arguments},
				})
			}
		}
		switch {
		case len(m.Parts) == 1 && m.Parts[0].Type == "text":
			om.Content = m.Parts[0].Text
		case len(m.Parts) > 0:
			parts := make([]map[string]any, 0, len(m.Parts))
			for _, p := range m.Parts {
				if p.Type == "image" {
					parts = append(parts, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:" + p.MimeType + ";base64," + p.Data,
						},
					})
					continue
				}
				parts = append(parts, map[string]any{"type": "text", "text": p.Text})
			}
			om.Content = parts
		}
		out = append(out, om)
	}
	return out
}

// summarizeError squeezes a vendor error body into one readable line.
func summarizeError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty response body)"
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message
		}
		if e.Message != "" {
			return e.Message
		}
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// readSSE is the shared SSE frame reader used by the Anthropic and Gemini
// clients. It yields the payload of each `data:` line.
func readSSE(ctx context.Context, r io.Reader, yield func(data []byte) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if !yield(payload) {
			return nil
		}
	}
	return scanner.Err()
}
