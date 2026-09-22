package agent

import (
	"strings"
	"testing"

	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

func TestSystemPromptIsStableAndDoesNotContainRuntimeState(t *testing.T) {
	first := buildSystemPrompt(promptContext{Mode: protocol.ModeWork})
	second := buildSystemPrompt(promptContext{Mode: protocol.ModeWork})
	if first != second {
		t.Fatal("stable system prompt changed between calls")
	}
	for _, forbidden := range []string{"当前时间", "操作系统", "当前模型", "可用知识库", "kb_search"} {
		if strings.Contains(first, forbidden) {
			t.Fatalf("stable prompt contains dynamic or legacy text %q", forbidden)
		}
	}
	if !strings.Contains(first, "skill_run") || !strings.Contains(first, "search") || !strings.Contains(first, "open") {
		t.Fatal("prompt is missing the minimal Knowledge Skill guidance")
	}
}

func TestMergeProviderUsagePreservesSplitCacheFrames(t *testing.T) {
	var usage provider.Usage
	mergeProviderUsage(&usage, provider.Usage{InputTokens: 100, CachedTokens: 40})
	mergeProviderUsage(&usage, provider.Usage{OutputTokens: 12})
	if usage.InputTokens != 100 || usage.CachedTokens != 40 || usage.OutputTokens != 12 {
		t.Fatalf("split usage frames were not merged: %+v", usage)
	}
}

func TestPromptPrefixHashUsesOnlyStableDimensions(t *testing.T) {
	base := promptPrefixHash("openai", "gpt-5", protocol.ModeWork)
	if base != promptPrefixHash("openai", "gpt-5", protocol.ModeWork) {
		t.Fatal("prefix hash is not deterministic")
	}
	for _, changed := range []string{
		promptPrefixHash("anthropic", "gpt-5", protocol.ModeWork),
		promptPrefixHash("openai", "gpt-4.1", protocol.ModeWork),
		promptPrefixHash("openai", "gpt-5", protocol.ModeCoding),
	} {
		if changed == base {
			t.Fatal("prefix hash did not change with a stable dimension")
		}
	}
}
