// Package provider holds HitK's model-vendor abstraction.
//
// Every cloud vendor is reduced to one interface, [Provider], whose only
// required capability is streaming a chat completion with tool calls. Vendors
// that speak the OpenAI wire format share a single implementation
// ([OpenAICompat]) parameterised by base URL, so adding DeepSeek or Kimi is a
// catalog entry rather than a new client.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Part is a content fragment. Only "text" and "image" are produced today, but
// the shape leaves room for file parts.
type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64 for images
}

// TextPart builds a text fragment.
func TextPart(s string) Part { return Part{Type: "text", Text: s} }

// ToolCallReq is a tool invocation requested by the model.
type ToolCallReq struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON, as emitted by the model
}

// Message is one entry of the conversation sent to a vendor.
type Message struct {
	Role       Role
	Parts      []Part
	ToolCalls  []ToolCallReq
	ToolCallID string // set when Role == RoleTool
	Name       string // set when Role == RoleTool
}

// Text flattens the text fragments of a message.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// UserText builds a plain user message.
func UserText(s string) Message { return Message{Role: RoleUser, Parts: []Part{TextPart(s)}} }

// AssistantText builds a plain assistant message.
func AssistantText(s string) Message { return Message{Role: RoleAssistant, Parts: []Part{TextPart(s)}} }

// AssistantTools builds an assistant message carrying tool calls.
func AssistantTools(calls []ToolCallReq, text string) Message {
	m := Message{Role: RoleAssistant, ToolCalls: calls}
	if text != "" {
		m.Parts = []Part{TextPart(text)}
	}
	return m
}

// ToolResult builds a tool result message.
func ToolResult(id, name, content string) Message {
	return Message{Role: RoleTool, ToolCallID: id, Name: name, Parts: []Part{TextPart(content)}}
}

// ToolSchema describes a callable tool to the vendor.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatRequest is one streaming completion request.
type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolSchema
	Temperature *float64
	MaxTokens   int
	// Metadata is passed through to the vendor when it supports it (OpenAI
	// `user`, OpenRouter `transforms`, …). Unknown keys are ignored.
	Metadata map[string]string
}

// ChunkKind discriminates stream chunks.
type ChunkKind string

const (
	// ChunkText is an incremental assistant text fragment.
	ChunkText ChunkKind = "text"
	// ChunkReasoning is an incremental chain-of-thought fragment, emitted by
	// vendors that expose reasoning (DeepSeek-R1, Anthropic thinking, o-series).
	ChunkReasoning ChunkKind = "reasoning"
	// ChunkToolCall is a completed tool invocation.
	ChunkToolCall ChunkKind = "tool_call"
	// ChunkUsage carries token accounting for the call so far.
	ChunkUsage ChunkKind = "usage"
	// ChunkDone terminates a stream without error.
	ChunkDone ChunkKind = "done"
	// ChunkError terminates a stream with a vendor-reported failure.
	ChunkError ChunkKind = "error"
)

// Chunk is a single streamed update.
type Chunk struct {
	Kind  ChunkKind
	Text  string
	Tool  *ToolCallReq
	Usage *Usage
	Err   error
}

// Usage is token accounting reported by a vendor.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
	TotalTokens  int
}

// Provider streams chat completions from one vendor/model family.
type Provider interface {
	// ID returns the catalog id ("openai", "deepseek", …).
	ID() string
	// Stream opens a completion stream. The returned channel is closed by the
	// implementation; a ChunkError is sent before closing on failure.
	Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
}

// ModelLister is implemented by providers that can enumerate their models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// Embedder turns text into vectors for knowledge-base retrieval.
type Embedder interface {
	ID() string
	Dimensions() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ErrNoCredentials is returned when a provider is used before its API key is
// configured.
var ErrNoCredentials = errors.New("provider: no API key configured")

// ErrNotSupported is returned for capabilities a vendor does not expose.
var ErrNotSupported = errors.New("provider: not supported")
