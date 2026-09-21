package provider

import (
	"sort"
	"strings"
)

// VendorKind selects the wire protocol a catalog entry speaks.
type VendorKind string

const (
	// KindOpenAI covers every vendor serving the OpenAI chat-completions API,
	// including the many Chinese clouds that ship a compatible endpoint.
	KindOpenAI VendorKind = "openai"
	// KindAnthropic is the Anthropic Messages API.
	KindAnthropic VendorKind = "anthropic"
	// KindGemini is Google's generateContent API.
	KindGemini VendorKind = "gemini"
	// KindOllama is a local Ollama daemon (OpenAI-compatible enough to reuse
	// the OpenAI client, but listed separately so the UI can skip the key UI).
	KindOllama VendorKind = "ollama"
)

// ModelPreset is a suggested model for a vendor. The list is a convenience for
// the picker only — users can always type an arbitrary model id, because
// vendors ship new models far faster than any embedded list can track.
type ModelPreset struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Context int    `json:"context,omitempty"`
	// Capabilities flags used by the UI to show badges.
	Reasoning bool `json:"reasoning,omitempty"`
	Vision    bool `json:"vision,omitempty"`
	Tools     bool `json:"tools,omitempty"`
	Embedding bool `json:"embedding,omitempty"`
}

// Vendor is a preset cloud model vendor.
type Vendor struct {
	ID           string        `json:"id"`
	Label        string        `json:"label"`
	Kind         VendorKind    `json:"kind"`
	BaseURL      string        `json:"baseUrl"`
	EnvKey       string        `json:"envKey"`
	APIKeyURL    string        `json:"apiKeyUrl,omitempty"`
	DocsURL      string        `json:"docsUrl,omitempty"`
	Region       string        `json:"region,omitempty"` // cn | global | local
	DefaultModel string        `json:"defaultModel"`
	Models       []ModelPreset `json:"models"`
	// EmbeddingBaseURL, when empty, means the vendor serves embeddings on the
	// same base URL as chat.
	EmbeddingBaseURL string `json:"embeddingBaseUrl,omitempty"`
	EmbeddingModel   string `json:"embeddingModel,omitempty"`
	// SupportsModelList is false for vendors with no live listing endpoint.
	SupportsModelList bool   `json:"supportsModelList"`
	Note              string `json:"note,omitempty"`
}

// Catalog is the full preset vendor list, ordered for display.
var Catalog = []Vendor{
	{
		ID: "openai", Label: "OpenAI", Kind: KindOpenAI,
		BaseURL: "https://api.openai.com/v1", EnvKey: "OPENAI_API_KEY",
		APIKeyURL: "https://platform.openai.com/api-keys",
		DocsURL:   "https://platform.openai.com/docs/api-reference",
		Region:    "global", DefaultModel: "gpt-5",
		SupportsModelList: true, EmbeddingModel: "text-embedding-3-small",
		Models: []ModelPreset{
			{ID: "gpt-5", Label: "GPT-5", Context: 400000, Reasoning: true, Vision: true, Tools: true},
			{ID: "gpt-5-mini", Label: "GPT-5 mini", Context: 400000, Reasoning: true, Vision: true, Tools: true},
			{ID: "gpt-5-nano", Label: "GPT-5 nano", Context: 400000, Reasoning: true, Tools: true},
			{ID: "gpt-4.1", Label: "GPT-4.1", Context: 1047576, Vision: true, Tools: true},
			{ID: "gpt-4.1-mini", Label: "GPT-4.1 mini", Context: 1047576, Vision: true, Tools: true},
			{ID: "gpt-4o", Label: "GPT-4o", Context: 128000, Vision: true, Tools: true},
			{ID: "o4-mini", Label: "o4-mini", Context: 200000, Reasoning: true, Tools: true},
			{ID: "text-embedding-3-small", Label: "text-embedding-3-small", Embedding: true},
			{ID: "text-embedding-3-large", Label: "text-embedding-3-large", Embedding: true},
		},
	},
	{
		ID: "anthropic", Label: "Anthropic Claude", Kind: KindAnthropic,
		BaseURL: "https://api.anthropic.com", EnvKey: "ANTHROPIC_API_KEY",
		APIKeyURL: "https://console.anthropic.com/settings/keys",
		DocsURL:   "https://docs.anthropic.com/en/api/messages",
		Region:    "global", DefaultModel: "claude-sonnet-4-5",
		SupportsModelList: true,
		Models: []ModelPreset{
			{ID: "claude-sonnet-4-5", Label: "Claude Sonnet 4.5", Context: 200000, Reasoning: true, Vision: true, Tools: true},
			{ID: "claude-opus-4-1", Label: "Claude Opus 4.1", Context: 200000, Reasoning: true, Vision: true, Tools: true},
			{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Context: 200000, Reasoning: true, Vision: true, Tools: true},
			{ID: "claude-3-7-sonnet-latest", Label: "Claude 3.7 Sonnet", Context: 200000, Reasoning: true, Vision: true, Tools: true},
		},
	},
	{
		ID: "gemini", Label: "Google Gemini", Kind: KindGemini,
		BaseURL: "https://generativelanguage.googleapis.com/v1beta", EnvKey: "GEMINI_API_KEY",
		APIKeyURL: "https://aistudio.google.com/apikey",
		DocsURL:   "https://ai.google.dev/api/generate-content",
		Region:    "global", DefaultModel: "gemini-2.5-pro",
		SupportsModelList: true,
		Models: []ModelPreset{
			{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Context: 1048576, Reasoning: true, Vision: true, Tools: true},
			{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Context: 1048576, Reasoning: true, Vision: true, Tools: true},
			{ID: "gemini-2.5-flash-lite", Label: "Gemini 2.5 Flash Lite", Context: 1048576, Reasoning: true, Vision: true, Tools: true},
			{ID: "text-embedding-004", Label: "text-embedding-004", Embedding: true},
		},
	},
	{
		ID: "deepseek", Label: "DeepSeek 深度求索", Kind: KindOpenAI,
		BaseURL: "https://api.deepseek.com/v1", EnvKey: "DEEPSEEK_API_KEY",
		APIKeyURL: "https://platform.deepseek.com/api_keys",
		DocsURL:   "https://api-docs.deepseek.com",
		Region:    "cn", DefaultModel: "deepseek-flash",
		SupportsModelList: true,
		Note: "旧模型名 deepseek-chat / deepseek-reasoner / deepseek-v4-flash 仍可调用，" +
			"但对应模型已下线，请求由在售模型接管。",
		Models: []ModelPreset{
			{ID: "deepseek-flash", Label: "DeepSeek V4.1 Flash", Context: 1048576, Reasoning: true, Vision: true, Tools: true},
			{ID: "deepseek-v4-pro", Label: "DeepSeek V4 Pro", Context: 1048576, Reasoning: true, Tools: true},
		},
	},
	{
		ID: "moonshot", Label: "月之暗面 Kimi", Kind: KindOpenAI,
		BaseURL: "https://api.moonshot.cn/v1", EnvKey: "MOONSHOT_API_KEY",
		APIKeyURL: "https://platform.moonshot.cn/console/api-keys",
		Region:    "cn", DefaultModel: "kimi-k2-0905-preview",
		SupportsModelList: true,
		Models: []ModelPreset{
			{ID: "kimi-k2-0905-preview", Label: "Kimi K2", Context: 256000, Tools: true},
			{ID: "kimi-k2-turbo-preview", Label: "Kimi K2 Turbo", Context: 256000, Tools: true},
			{ID: "moonshot-v1-128k", Label: "Moonshot v1 128k", Context: 131072, Tools: true},
			{ID: "moonshot-v1-32k", Label: "Moonshot v1 32k", Context: 32768, Tools: true},
		},
	},
	{
		ID: "zhipu", Label: "智谱 GLM", Kind: KindOpenAI,
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", EnvKey: "ZHIPU_API_KEY",
		APIKeyURL: "https://open.bigmodel.cn/usercenter/apikeys",
		Region:    "cn", DefaultModel: "glm-4.6",
		SupportsModelList: false, EmbeddingModel: "embedding-3",
		Models: []ModelPreset{
			{ID: "glm-4.6", Label: "GLM-4.6", Context: 200000, Reasoning: true, Tools: true},
			{ID: "glm-4.5", Label: "GLM-4.5", Context: 128000, Reasoning: true, Tools: true},
			{ID: "glm-4.5-air", Label: "GLM-4.5 Air", Context: 128000, Reasoning: true, Tools: true},
			{ID: "glm-4v-plus", Label: "GLM-4V Plus", Context: 8192, Vision: true, Tools: true},
			{ID: "embedding-3", Label: "embedding-3", Embedding: true},
		},
	},
	{
		ID: "dashscope", Label: "阿里云通义千问", Kind: KindOpenAI,
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", EnvKey: "DASHSCOPE_API_KEY",
		APIKeyURL: "https://bailian.console.aliyun.com/?apiKey=1",
		Region:    "cn", DefaultModel: "qwen3-max",
		SupportsModelList: true, EmbeddingModel: "text-embedding-v4",
		Models: []ModelPreset{
			{ID: "qwen3-max", Label: "Qwen3 Max", Context: 262144, Reasoning: true, Tools: true},
			{ID: "qwen-plus", Label: "Qwen Plus", Context: 131072, Reasoning: true, Tools: true},
			{ID: "qwen-turbo", Label: "Qwen Turbo", Context: 1000000, Tools: true},
			{ID: "qwen3-coder-plus", Label: "Qwen3 Coder Plus", Context: 1048576, Tools: true},
			{ID: "qwen-vl-max", Label: "Qwen VL Max", Context: 131072, Vision: true, Tools: true},
			{ID: "text-embedding-v4", Label: "text-embedding-v4", Embedding: true},
		},
	},
	{
		ID: "doubao", Label: "字节豆包 Doubao", Kind: KindOpenAI,
		BaseURL: "https://ark.cn-beijing.volces.com/api/v3", EnvKey: "ARK_API_KEY",
		APIKeyURL: "https://console.volcengine.com/ark",
		Region:    "cn", DefaultModel: "doubao-seed-1-6-250615",
		SupportsModelList: false, EmbeddingModel: "doubao-embedding-text-240715",
		Note: "模型 id 需填接入点（endpoint）或模型名",
		Models: []ModelPreset{
			{ID: "doubao-seed-1-6-250615", Label: "Doubao Seed 1.6", Context: 256000, Reasoning: true, Vision: true, Tools: true},
			{ID: "doubao-seed-1-6-flash-250715", Label: "Doubao Seed 1.6 Flash", Context: 256000, Reasoning: true, Tools: true},
			{ID: "doubao-1-5-pro-32k-250115", Label: "Doubao 1.5 Pro 32k", Context: 32768, Tools: true},
			{ID: "doubao-embedding-text-240715", Label: "doubao-embedding-text", Embedding: true},
		},
	},
	{
		// Agent Plan has its own subscription endpoint. It is OpenAI-compatible,
		// but does not expose a /models endpoint, so keep its official model
		// directory in the application instead of attempting remote discovery.
		ID: "volcengine-plan", Label: "火山方舟 Agent Plan", Kind: KindOpenAI,
		BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", EnvKey: "ARK_API_KEY",
		APIKeyURL: "https://console.volcengine.com/ark/region:cn-beijing/subscription/agent-plan?agentMode=close",
		Region:    "cn", DefaultModel: "ark-code-latest", SupportsModelList: false,
		Note: "Agent Plan 套餐专属 OpenAI 兼容入口，使用 /api/plan/v3。",
		Models: []ModelPreset{
			{ID: "ark-code-latest", Label: "Ark Code Latest", Context: 200000, Reasoning: true},
			{ID: "doubao-seed-evolving", Label: "Doubao Seed Evolving", Context: 200000, Reasoning: true},
			{ID: "doubao-seed-2.0-mini", Label: "Doubao Seed 2.0 Mini", Context: 1000000, Reasoning: true},
			{ID: "doubao-seed-2.1-turbo", Label: "Doubao Seed 2.1 Turbo", Context: 1000000, Reasoning: true},
			{ID: "doubao-seed-2.0-lite", Label: "Doubao Seed 2.0 Lite", Context: 32768, Reasoning: true},
			{ID: "minimax-m3", Label: "MiniMax M3", Context: 1000000, Reasoning: true},
			{ID: "glm-5.3", Label: "GLM-5.3", Context: 1000000, Reasoning: true},
			{ID: "glm-latest", Label: "GLM Latest", Context: 1000000, Reasoning: true},
			{ID: "glm-5.3-flash", Label: "GLM-5.3 Flash", Context: 1000000, Reasoning: true, Vision: true},
			{ID: "deepseek-v4-flash", Label: "DeepSeek V4 Flash", Context: 1000000, Reasoning: true},
			{ID: "deepseek-v4-pro", Label: "DeepSeek V4 Pro", Context: 1000000, Reasoning: true},
			{ID: "kimi-k2.7-code", Label: "Kimi K2.7 Code", Context: 256000, Reasoning: true},
			{ID: "kimi-k3", Label: "Kimi K3", Context: 1000000, Reasoning: true},
		},
	},
	{
		ID: "hunyuan", Label: "腾讯混元 Hunyuan", Kind: KindOpenAI,
		BaseURL: "https://api.hunyuan.cloud.tencent.com/v1", EnvKey: "HUNYUAN_API_KEY",
		APIKeyURL: "https://console.cloud.tencent.com/hunyuan/api-key",
		Region:    "cn", DefaultModel: "hunyuan-turbos-latest",
		SupportsModelList: false, EmbeddingModel: "hunyuan-embedding",
		Models: []ModelPreset{
			{ID: "hunyuan-turbos-latest", Label: "混元 TurboS", Context: 32768, Tools: true},
			{ID: "hunyuan-large", Label: "混元 Large", Context: 32768, Tools: true},
			{ID: "hunyuan-t1-latest", Label: "混元 T1 (推理)", Context: 131072, Reasoning: true, Tools: true},
			{ID: "hunyuan-embedding", Label: "hunyuan-embedding", Embedding: true},
		},
	},
	{
		ID: "qianfan", Label: "百度文心千帆", Kind: KindOpenAI,
		BaseURL: "https://qianfan.baidubce.com/v2", EnvKey: "QIANFAN_API_KEY",
		APIKeyURL: "https://console.bce.baidu.com/iam/#/iam/apikey/list",
		Region:    "cn", DefaultModel: "ernie-4.5-turbo-128k",
		SupportsModelList: false, EmbeddingModel: "bge-large-zh",
		Models: []ModelPreset{
			{ID: "ernie-4.5-turbo-128k", Label: "ERNIE 4.5 Turbo 128k", Context: 131072, Tools: true},
			{ID: "ernie-x1-turbo-32k", Label: "ERNIE X1 Turbo (推理)", Context: 32768, Reasoning: true, Tools: true},
			{ID: "bge-large-zh", Label: "bge-large-zh", Embedding: true},
		},
	},
	{
		ID: "minimax", Label: "MiniMax", Kind: KindOpenAI,
		BaseURL: "https://api.minimax.chat/v1", EnvKey: "MINIMAX_API_KEY",
		APIKeyURL: "https://platform.minimaxi.com/user-center/basic-information/interface-key",
		Region:    "cn", DefaultModel: "MiniMax-M2",
		SupportsModelList: false, EmbeddingModel: "embo-01",
		Models: []ModelPreset{
			{ID: "MiniMax-M2", Label: "MiniMax M2", Context: 204800, Reasoning: true, Tools: true},
			{ID: "MiniMax-Text-01", Label: "MiniMax Text 01", Context: 1000000, Tools: true},
			{ID: "embo-01", Label: "embo-01", Embedding: true},
		},
	},
	{
		ID: "siliconflow", Label: "硅基流动 SiliconFlow", Kind: KindOpenAI,
		BaseURL: "https://api.siliconflow.cn/v1", EnvKey: "SILICONFLOW_API_KEY",
		APIKeyURL: "https://cloud.siliconflow.cn/account/ak",
		Region:    "cn", DefaultModel: "Qwen/Qwen3-235B-A22B-Instruct-2507",
		SupportsModelList: true, EmbeddingModel: "BAAI/bge-m3",
		Models: []ModelPreset{
			{ID: "Qwen/Qwen3-235B-A22B-Instruct-2507", Label: "Qwen3 235B", Context: 262144, Tools: true},
			{ID: "deepseek-ai/DeepSeek-V3.2-Exp", Label: "DeepSeek V3.2 Exp", Context: 163840, Tools: true},
			{ID: "zai-org/GLM-4.6", Label: "GLM-4.6", Context: 200000, Reasoning: true, Tools: true},
			{ID: "moonshotai/Kimi-K2-Instruct-0905", Label: "Kimi K2 Instruct", Context: 262144, Tools: true},
			{ID: "BAAI/bge-m3", Label: "bge-m3", Embedding: true},
		},
	},
	{
		ID: "openrouter", Label: "OpenRouter", Kind: KindOpenAI,
		BaseURL: "https://openrouter.ai/api/v1", EnvKey: "OPENROUTER_API_KEY",
		APIKeyURL: "https://openrouter.ai/keys",
		Region:    "global", DefaultModel: "anthropic/claude-sonnet-4.5",
		SupportsModelList: true,
		Models: []ModelPreset{
			{ID: "anthropic/claude-sonnet-4.5", Label: "Claude Sonnet 4.5", Context: 200000, Reasoning: true, Vision: true, Tools: true},
			{ID: "openai/gpt-5", Label: "GPT-5", Context: 400000, Reasoning: true, Vision: true, Tools: true},
			{ID: "google/gemini-2.5-pro", Label: "Gemini 2.5 Pro", Context: 1048576, Reasoning: true, Vision: true, Tools: true},
			{ID: "deepseek/deepseek-chat-v3.1", Label: "DeepSeek V3.1", Context: 163840, Tools: true},
			{ID: "qwen/qwen3-max", Label: "Qwen3 Max", Context: 262144, Tools: true},
			{ID: "x-ai/grok-4", Label: "Grok 4", Context: 256000, Reasoning: true, Tools: true},
		},
	},
	{
		ID: "xai", Label: "xAI Grok", Kind: KindOpenAI,
		BaseURL: "https://api.x.ai/v1", EnvKey: "XAI_API_KEY",
		APIKeyURL: "https://console.x.ai",
		Region:    "global", DefaultModel: "grok-4",
		SupportsModelList: true,
		Models: []ModelPreset{
			{ID: "grok-4", Label: "Grok 4", Context: 256000, Reasoning: true, Vision: true, Tools: true},
			{ID: "grok-4-fast", Label: "Grok 4 Fast", Context: 2000000, Reasoning: true, Vision: true, Tools: true},
			{ID: "grok-3-mini", Label: "Grok 3 Mini", Context: 131072, Reasoning: true, Tools: true},
		},
	},
	{
		ID: "mistral", Label: "Mistral AI", Kind: KindOpenAI,
		BaseURL: "https://api.mistral.ai/v1", EnvKey: "MISTRAL_API_KEY",
		APIKeyURL: "https://console.mistral.ai/api-keys",
		Region:    "global", DefaultModel: "mistral-large-latest",
		SupportsModelList: true, EmbeddingModel: "mistral-embed",
		Models: []ModelPreset{
			{ID: "mistral-large-latest", Label: "Mistral Large", Context: 131072, Tools: true},
			{ID: "mistral-medium-latest", Label: "Mistral Medium", Context: 131072, Vision: true, Tools: true},
			{ID: "magistral-medium-latest", Label: "Magistral Medium", Context: 131072, Reasoning: true, Tools: true},
			{ID: "codestral-latest", Label: "Codestral", Context: 262144, Tools: true},
			{ID: "mistral-embed", Label: "mistral-embed", Embedding: true},
		},
	},
	{
		ID: "groq", Label: "Groq", Kind: KindOpenAI,
		BaseURL: "https://api.groq.com/openai/v1", EnvKey: "GROQ_API_KEY",
		APIKeyURL: "https://console.groq.com/keys",
		Region:    "global", DefaultModel: "llama-3.3-70b-versatile",
		SupportsModelList: true, Note: "超低延迟推理",
		Models: []ModelPreset{
			{ID: "llama-3.3-70b-versatile", Label: "Llama 3.3 70B", Context: 131072, Tools: true},
			{ID: "openai/gpt-oss-120b", Label: "GPT-OSS 120B", Context: 131072, Reasoning: true, Tools: true},
			{ID: "moonshotai/kimi-k2-instruct", Label: "Kimi K2", Context: 131072, Tools: true},
		},
	},
	{
		ID: "ollama", Label: "Ollama 本地", Kind: KindOllama,
		BaseURL: "http://127.0.0.1:11434/v1", EnvKey: "",
		Region: "local", DefaultModel: "qwen3:8b",
		SupportsModelList: true, EmbeddingModel: "nomic-embed-text",
		Note: "需本地运行 ollama serve，无需 API Key",
		Models: []ModelPreset{
			{ID: "qwen3:8b", Label: "Qwen3 8B", Context: 32768, Reasoning: true, Tools: true},
			{ID: "qwen2.5-coder:7b", Label: "Qwen2.5 Coder 7B", Context: 32768, Tools: true},
			{ID: "llama3.1:8b", Label: "Llama 3.1 8B", Context: 131072, Tools: true},
			{ID: "deepseek-r1:8b", Label: "DeepSeek R1 8B", Context: 131072, Reasoning: true},
			{ID: "nomic-embed-text", Label: "nomic-embed-text", Embedding: true},
		},
	},
	{
		ID: "custom", Label: "自定义 (OpenAI 兼容)", Kind: KindOpenAI,
		BaseURL: "", EnvKey: "",
		Region: "custom", DefaultModel: "",
		SupportsModelList: true,
		Note:              "任意兼容 /v1/chat/completions 的服务，如 LM Studio、vLLM、one-api",
		Models:            []ModelPreset{},
	},
}

// VendorByID looks up a catalog entry.
func VendorByID(id string) (Vendor, bool) {
	for _, v := range Catalog {
		if v.ID == id {
			return v, true
		}
	}
	return Vendor{}, false
}

// CatalogByRegion groups the catalog for the settings UI.
func CatalogByRegion() map[string][]Vendor {
	out := map[string][]Vendor{}
	for _, v := range Catalog {
		out[v.Region] = append(out[v.Region], v)
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	}
	return out
}

// RegionLabels are the human-readable region group titles.
var RegionLabels = map[string]string{
	"cn":     "国内云厂商",
	"global": "海外云厂商",
	"local":  "本地推理",
	"custom": "自定义接入",
}

// ModelPresetByID resolves one catalog model, reporting false for ids the
// catalog does not know (custom models, or a vendor's brand-new release).
func ModelPresetByID(vendorID, model string) (ModelPreset, bool) {
	meta, ok := VendorByID(vendorID)
	if !ok {
		return ModelPreset{}, false
	}
	for _, preset := range meta.Models {
		if preset.ID == model {
			return preset, true
		}
	}
	return ModelPreset{}, false
}

// retiredModels maps a vendor's withdrawn model ids onto the id now served in
// their place. Vendors rename models far more often than HitK ships releases,
// and a saved default pointing at a retired id leaves every turn failing until
// the user happens to open the settings page and re-pick a model by hand.
var retiredModels = map[string]map[string]string{
	"deepseek": {
		// V3/R1-era names. chat was the general-purpose endpoint and reasoner
		// the flagship, so they map onto the two models on sale today rather
		// than both collapsing onto the default.
		"deepseek-chat":     "deepseek-flash",
		"deepseek-reasoner": "deepseek-v4-pro",
		// V4-Flash first shipped under these names; the model was folded into
		// deepseek-flash and requests are already served by it.
		"deepseek-v4-flash":            "deepseek-flash",
		"deepseek-v4-flash-vision-exp": "deepseek-flash",
	},
}

// ReplacementModel returns the model id that took over from a retired one, or
// "" when the id is still current.
//
// This is a lookup table on purpose, not "any id missing from the catalog":
// users may enable models the catalog has never heard of (a self-hosted build,
// a vendor's unreleased model), and rewriting those would clobber a deliberate
// choice we have no replacement for.
func ReplacementModel(vendorID, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	return retiredModels[vendorID][model]
}

// NormalizeVendorModels rewrites retired model ids on a saved vendor config.
// It reports whether anything changed, so the caller can persist the result
// once instead of re-deriving the same fix on every start.
func NormalizeVendorModels(cfg *VendorConfig) bool {
	changed := false
	if next := ReplacementModel(cfg.ID, cfg.DefaultModel); next != "" {
		cfg.DefaultModel = next
		changed = true
	}
	for i := range cfg.EnabledModels {
		next := ReplacementModel(cfg.ID, cfg.EnabledModels[i].ID)
		if next == "" {
			continue
		}
		cfg.EnabledModels[i].ID = next
		// The snapshot was captured when the retired model was enabled, so its
		// label and badges would contradict the new id in the UI.
		if preset, ok := ModelPresetByID(cfg.ID, next); ok {
			ref := &cfg.EnabledModels[i]
			ref.Label = preset.Label
			ref.Reasoning = preset.Reasoning
			ref.Vision = preset.Vision
			ref.Tools = preset.Tools
			ref.Context = preset.Context
		}
		changed = true
	}
	return changed
}

// GuessVendorFromModel infers a vendor id from a bare model name, so a pasted
// model id lands in the right picker group.
func GuessVendorFromModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "kimi"), strings.HasPrefix(m, "moonshot"):
		return "moonshot"
	case strings.HasPrefix(m, "glm"):
		return "zhipu"
	case strings.HasPrefix(m, "qwen"), strings.HasPrefix(m, "qwq"):
		return "dashscope"
	case strings.HasPrefix(m, "doubao"):
		return "doubao"
	case strings.HasPrefix(m, "hunyuan"):
		return "hunyuan"
	case strings.HasPrefix(m, "ernie"):
		return "qianfan"
	case strings.HasPrefix(m, "minimax"), strings.HasPrefix(m, "abab"):
		return "minimax"
	case strings.HasPrefix(m, "grok"):
		return "xai"
	case strings.HasPrefix(m, "mistral"), strings.HasPrefix(m, "magistral"), strings.HasPrefix(m, "codestral"):
		return "mistral"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "openai"
	}
	return ""
}
