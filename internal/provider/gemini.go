package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Gemini implements Google's generateContent API.
type Gemini struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// GeminiConfig configures the client.
type GeminiConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// NewGemini builds a Gemini client.
func NewGemini(cfg GeminiConfig) *Gemini {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta"
	}
	t := cfg.Timeout
	if t == 0 {
		t = 30 * time.Minute
	}
	return &Gemini{baseURL: base, apiKey: cfg.APIKey, client: &http.Client{Timeout: t}}
}

// ID implements [Provider].
func (g *Gemini) ID() string { return "gemini" }

type gemRequest struct {
	SystemInstruction *gemContent    `json:"systemInstruction,omitempty"`
	Contents          []gemContent   `json:"contents"`
	Tools             []gemToolGroup `json:"tools,omitempty"`
	GenerationConfig  *gemGenConfig  `json:"generationConfig,omitempty"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []gemPart `json:"parts"`
}

type gemPart struct {
	Text             string         `json:"text,omitempty"`
	FunctionCall     *gemFuncCall   `json:"functionCall,omitempty"`
	FunctionResponse *gemFuncResp   `json:"functionResponse,omitempty"`
	InlineData       *gemInlineData `json:"inlineData,omitempty"`
	Thought          bool           `json:"thought,omitempty"`
}

type gemFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gemFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gemInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemToolGroup struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations"`
}

type gemFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type gemGenConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

// Stream implements [Provider].
func (g *Gemini) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if g.apiKey == "" {
		return nil, ErrNoCredentials
	}

	body := gemRequest{Contents: toGemContents(req.Messages)}
	if strings.TrimSpace(req.System) != "" {
		body.SystemInstruction = &gemContent{Parts: []gemPart{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		group := gemToolGroup{}
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				// Gemini rejects an omitted `parameters` on some models and an
				// empty object on others; a permissive schema satisfies both.
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			group.FunctionDeclarations = append(group.FunctionDeclarations, gemFuncDecl{
				Name: t.Name, Description: t.Description, Parameters: params,
			})
		}
		body.Tools = []gemToolGroup{group}
	}
	if req.MaxTokens > 0 || req.Temperature != nil {
		body.GenerationConfig = &gemGenConfig{MaxOutputTokens: req.MaxTokens, Temperature: req.Temperature}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse",
		g.baseURL, url.PathEscape(strings.TrimPrefix(req.Model, "models/")))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, summarizeError(msg))
	}

	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		g.pump(ctx, resp.Body, out)
	}()
	return out, nil
}

func (g *Gemini) pump(ctx context.Context, r io.Reader, out chan<- Chunk) {
	// Gemini identifies function calls by name only; synthesise stable ids so
	// the loop can pair each result with its call.
	callSeq := 0

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
			Candidates []struct {
				Content struct {
					Parts []gemPart `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
				CachedContentCount   int `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
			Error *struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			return true
		}
		if frame.Error != nil {
			send(Chunk{Kind: ChunkError, Err: fmt.Errorf("gemini: %s", frame.Error.Message)})
			return false
		}
		if frame.UsageMetadata != nil {
			u := &Usage{
				InputTokens:  frame.UsageMetadata.PromptTokenCount,
				OutputTokens: frame.UsageMetadata.CandidatesTokenCount,
				TotalTokens:  frame.UsageMetadata.TotalTokenCount,
				CachedTokens: frame.UsageMetadata.CachedContentCount,
			}
			if u.TotalTokens == 0 {
				u.TotalTokens = u.InputTokens + u.OutputTokens
			}
			send(Chunk{Kind: ChunkUsage, Usage: u})
		}
		for _, cand := range frame.Candidates {
			for _, part := range cand.Content.Parts {
				switch {
				case part.FunctionCall != nil && part.FunctionCall.Name != "":
					args := strings.TrimSpace(string(part.FunctionCall.Args))
					if args == "" {
						args = "{}"
					}
					callSeq++
					if !send(Chunk{Kind: ChunkToolCall, Tool: &ToolCallReq{
						ID:        fmt.Sprintf("call_%d", callSeq),
						Name:      part.FunctionCall.Name,
						Arguments: args,
					}}) {
						return false
					}
				case part.Thought:
					if part.Text != "" && !send(Chunk{Kind: ChunkReasoning, Text: part.Text}) {
						return false
					}
				case part.Text != "":
					if !send(Chunk{Kind: ChunkText, Text: part.Text}) {
						return false
					}
				}
			}
		}
		return true
	})
	if err != nil && ctx.Err() == nil {
		send(Chunk{Kind: ChunkError, Err: fmt.Errorf("gemini: %w", err)})
		return
	}
	send(Chunk{Kind: ChunkDone})
}

// toGemContents converts HitK messages to Gemini's contents array. Tool
// results become functionResponse parts inside a user turn.
func toGemContents(msgs []Message) []gemContent {
	var out []gemContent
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			continue // hoisted into systemInstruction
		case RoleTool:
			part := gemPart{FunctionResponse: &gemFuncResp{
				Name:     m.Name,
				Response: map[string]any{"result": m.Text()},
			}}
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				out[n-1].Parts = append(out[n-1].Parts, part)
				continue
			}
			out = append(out, gemContent{Role: "user", Parts: []gemPart{part}})
		case RoleAssistant:
			parts := make([]gemPart, 0, len(m.Parts)+len(m.ToolCalls))
			for _, p := range m.Parts {
				if p.Type == "text" && p.Text != "" {
					parts = append(parts, gemPart{Text: p.Text})
				}
			}
			for _, tc := range m.ToolCalls {
				args := json.RawMessage(tc.Arguments)
				if len(args) == 0 || !json.Valid(args) {
					args = json.RawMessage(`{}`)
				}
				parts = append(parts, gemPart{FunctionCall: &gemFuncCall{Name: tc.Name, Args: args}})
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, gemContent{Role: "model", Parts: parts})
		default: // user
			parts := make([]gemPart, 0, len(m.Parts))
			for _, p := range m.Parts {
				if p.Type == "image" && p.Data != "" {
					parts = append(parts, gemPart{InlineData: &gemInlineData{MimeType: p.MimeType, Data: p.Data}})
					continue
				}
				parts = append(parts, gemPart{Text: p.Text})
			}
			if len(parts) == 0 {
				parts = append(parts, gemPart{Text: ""})
			}
			out = append(out, gemContent{Role: "user", Parts: parts})
		}
	}
	return out
}
