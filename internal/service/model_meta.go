package service

import (
	"strings"

	"github.com/hitk/hitk/internal/provider"
)

// contextLimitFor resolves a model's context window.
//
// Vendors expose no uniform way to ask, so HitK uses the catalog preset when
// one matches and otherwise guesses from the model name. The value is only used
// to decide when to compact the conversation, and a wrong-but-conservative
// number costs a little history rather than a failed request.
func contextLimitFor(vendorID, model string) int {
	if meta, ok := provider.VendorByID(vendorID); ok {
		for _, m := range meta.Models {
			if m.ID == model && m.Context > 0 {
				return m.Context
			}
		}
	}
	// Embedding models sometimes get selected by mistake; give them a small
	// budget so the failure is obvious rather than a huge silent context.
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "embedding"), strings.Contains(lower, "embed"):
		return 8192
	case strings.Contains(lower, "gemini"), strings.Contains(lower, "claude"):
		return 200000
	case strings.Contains(lower, "qwen3-coder"), strings.Contains(lower, "qwen-turbo"):
		return 1000000
	case strings.Contains(lower, "gpt-5"), strings.Contains(lower, "gpt-4.1"):
		return 400000
	case strings.Contains(lower, "gpt-4o"):
		return 128000
	}
	// A conservative default: most current models comfortably fit this, and
	// compacting slightly early is harmless.
	return 128000
}
