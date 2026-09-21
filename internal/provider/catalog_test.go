package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVolcenginePlanCatalogAndClient(t *testing.T) {
	meta, ok := VendorByID("volcengine-plan")
	if !ok {
		t.Fatal("Agent Plan is missing from the provider catalog")
	}
	if meta.Kind != KindOpenAI {
		t.Fatalf("Agent Plan kind = %q, want OpenAI compatibility", meta.Kind)
	}
	if meta.BaseURL != "https://ark.cn-beijing.volces.com/api/plan/v3" {
		t.Fatalf("Agent Plan base URL = %q", meta.BaseURL)
	}
	if meta.SupportsModelList {
		t.Fatal("Agent Plan must not call its unsupported /models endpoint")
	}

	client, err := Build(VendorConfig{ID: meta.ID, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("build Agent Plan client: %v", err)
	}
	openAI, ok := client.(*OpenAICompat)
	if !ok {
		t.Fatalf("Agent Plan client = %T, want *OpenAICompat", client)
	}
	if openAI.BaseURL() != meta.BaseURL {
		t.Fatalf("client base URL = %q, want %q", openAI.BaseURL(), meta.BaseURL)
	}
}

func TestVolcenginePlanListsBuiltInModels(t *testing.T) {
	names, err := ListModels(context.Background(), VendorConfig{ID: "volcengine-plan"})
	if err != nil {
		t.Fatalf("list Agent Plan models: %v", err)
	}
	if len(names) != 13 {
		t.Fatalf("Agent Plan model count = %d, want 13", len(names))
	}
	if names[0] != "ark-code-latest" {
		t.Fatalf("first sorted model = %q, want ark-code-latest", names[0])
	}
}

func TestOpenAICompatForwardsReasoningEffort(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("request path = %q, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewOpenAICompat(OpenAICompatConfig{
		ID: "volcengine-plan", BaseURL: server.URL, APIKey: "test-key",
	})
	stream, err := client.Stream(context.Background(), ChatRequest{
		Model: "ark-code-latest", Messages: []Message{UserText("hello")},
		Metadata: map[string]string{"reasoning_effort": "high"},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream {
	}
	body := <-requests
	if got := body["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", got)
	}
}

func TestOpenAICompatForwardsThinkingDisabled(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewOpenAICompat(OpenAICompatConfig{
		ID: "volcengine-plan", BaseURL: server.URL, APIKey: "test-key",
	})
	stream, err := client.Stream(context.Background(), ChatRequest{
		Model: "deepseek-v4-pro", Messages: []Message{UserText("hello")},
		Metadata: map[string]string{"thinking_type": "disabled"},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream {
	}
	body := <-requests
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v, want type disabled", body["thinking"])
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort must be omitted when thinking is disabled: %#v", body)
	}
}
