package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hitk/hitk/internal/agent"
	"github.com/hitk/hitk/internal/companion"
	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// companionModelBridge adapts the configured LLM to the companion module's
// two model ports. The companion package never imports provider or config.
type companionModelBridge struct {
	providers *provider.Registry
	config    *config.Store
	callMu    sync.Mutex
}

// modelChoice is a resolved provider/model pair for one companion call.
type modelChoice struct {
	provider string
	model    string
}

func (c modelChoice) String() string { return c.provider + "/" + c.model }

// resolveModel prefers the character's own pinned model and falls back to the
// companion mode default when it is unset or points at a provider that is no
// longer usable. The fallback is resolved per call, so a deleted or disabled
// provider self-heals without any data migration.
func (b *companionModelBridge) resolveModel(profile companion.Profile) modelChoice {
	fallback := func() modelChoice {
		choice := b.config.ModeDefault(protocol.ModeCompanion)
		return modelChoice{provider: choice.Provider, model: choice.Model}
	}
	if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
		return fallback()
	}
	if !b.providers.Ready(profile.Provider) {
		return fallback()
	}
	return modelChoice{provider: profile.Provider, model: profile.Model}
}

// Available reports whether any configured provider can serve companion calls.
// Per-character resolution happens later, so this stays a coarse gate.
func (b *companionModelBridge) Available() bool {
	for _, cfg := range b.providers.Configs() {
		if b.providers.Ready(cfg.ID) {
			return true
		}
	}
	return false
}

func (b *companionModelBridge) Decide(ctx context.Context, input companion.DecisionInput) (companion.ProactiveDecision, error) {
	raw, err := b.completeJSON(
		ctx,
		b.resolveModel(input.Profile),
		input.Profile.ReasoningEffort,
		decisionSystemPrompt(input.Profile),
		decisionUserPrompt(input),
		700,
		"report_proactive_decision",
		"Return whether the companion should proactively send a message.",
		json.RawMessage(`{"type":"object","properties":{"send":{"type":"boolean"},"message":{"type":"string"},"reason":{"type":"string"}},"required":["send","reason"]}`),
	)
	if err != nil {
		return companion.ProactiveDecision{}, err
	}
	var decision companion.ProactiveDecision
	if err := decodeModelJSON(raw, &decision); err != nil {
		return companion.ProactiveDecision{}, err
	}
	if !decision.Send {
		decision.Message = ""
	}
	return decision, nil
}

func (b *companionModelBridge) ExtractMemories(ctx context.Context, input companion.MemoryInput) ([]companion.MemoryCandidate, error) {
	raw, err := b.completeJSON(
		ctx,
		b.resolveModel(input.Profile),
		input.Profile.ReasoningEffort,
		memorySystemPrompt(),
		memoryUserPrompt(input),
		900,
		"report_memory_candidates",
		"Return durable memories extracted from the conversation.",
		json.RawMessage(`{"type":"object","properties":{"memories":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},"content":{"type":"string"},"importance":{"type":"number"}},"required":["kind","content","importance"]}}},"required":["memories"]}`),
	)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Memories []companion.MemoryCandidate `json:"memories"`
	}
	if err := decodeModelJSON(raw, &payload); err != nil {
		return nil, err
	}
	out := make([]companion.MemoryCandidate, 0, min(len(payload.Memories), 3))
	for _, memory := range payload.Memories {
		memory.Content = strings.TrimSpace(memory.Content)
		if memory.Content == "" {
			continue
		}
		memory.Content = clipText(memory.Content, 240)
		out = append(out, memory)
		if len(out) == 3 {
			break
		}
	}
	return out, nil
}

func (b *companionModelBridge) AlignTurn(ctx context.Context, input companion.TurnAlignmentInput) (companion.TurnBrief, error) {
	raw, err := b.completeJSON(
		ctx,
		b.resolveModel(input.Profile),
		"off",
		turnAlignmentSystemPrompt(),
		turnAlignmentUserPrompt(input),
		1800,
		"align_companion_turn",
		"Return the validated turn brief before companion narration is generated.",
		json.RawMessage(`{
		  "type":"object",
		  "properties":{
		    "clock":{"type":"object","properties":{"now":{"type":"number"},"localTime":{"type":"string"},"elapsedMinutes":{"type":"number"},"absenceHours":{"type":"number"},"daysCrossed":{"type":"number"},"activity":{"type":"string"},"available":{"type":"boolean"},"availabilityNote":{"type":"string"},"nextAvailableAt":{"type":"number"}}},
		    "scene":{"type":"object","properties":{"stage":{"type":"string"},"consentState":{"type":"string"},"startedAt":{"type":"number"},"durationMinutes":{"type":"number"},"location":{"type":"string"},"participants":{"type":"array","items":{"type":"string"}},"physicalState":{"type":"string"},"continuityNote":{"type":"string"},"timeConstraintNote":{"type":"string"},"autoAdvancedByClock":{"type":"boolean"}}},
		    "input":{"type":"object","properties":{"intent":{"type":"string"},"explicitRequests":{"type":"array","items":{"type":"string"}},"impliedNeeds":{"type":"array","items":{"type":"string"}},"boundaries":{"type":"array","items":{"type":"string"}},"roleplayNote":{"type":"string"},"factClaims":{"type":"array","items":{"type":"string"}},"missingDetails":{"type":"array","items":{"type":"string"}},"ambiguities":{"type":"array","items":{"type":"string"}}}},
		    "resolution":{"type":"object","properties":{"actionMinutes":{"type":"number"},"sceneStage":{"type":"string"},"consentState":{"type":"string"},"impulses":{"type":"object","additionalProperties":{"type":"number"}},"traitChanges":{"type":"object","additionalProperties":{"type":"number"}},"effects":{"type":"array","items":{"type":"object","properties":{"target":{"type":"string"},"ratePerHour":{"type":"number"},"baselineDelta":{"type":"number"},"recoveryDelta":{"type":"number"},"durationMinutes":{"type":"number"},"curve":{"type":"string"}}}},"events":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string"},"content":{"type":"string"},"importance":{"type":"number"},"shareable":{"type":"boolean"},"deltas":{"type":"object","additionalProperties":{"type":"number"}}}}},"memories":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string"},"content":{"type":"string"},"importance":{"type":"number"}}}},"thoughts":{"type":"array","items":{"type":"string"}},"commitments":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["work","meal","exam","dinner","trip","errand","social","user"]},"label":{"type":"string"},"date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},"start":{"type":"string","pattern":"^[0-9]{2}:[0-9]{2}$"},"duration":{"type":"number"},"weight":{"type":"number"},"city":{"type":"string"}},"required":["label","date"]}}},"world":{"type":"object","properties":{"arrangements":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["work","meal","exam","dinner","trip","errand","social","user"]},"label":{"type":"string"},"date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},"start":{"type":"string","pattern":"^[0-9]{2}:[0-9]{2}$"},"duration":{"type":"number"},"weight":{"type":"number"},"city":{"type":"string"},"mustAttend":{"type":"boolean"}},"required":["label","date"]}},"disruptions":{"type":"array","items":{"type":"object","properties":{"start":{"type":"string","pattern":"^[0-9]{2}:[0-9]{2}$"},"minutes":{"type":"number"},"severity":{"type":"number"},"reason":{"type":"string"},"startBlock":{"type":"string"},"completeBlock":{"type":"string"}},"required":["reason","minutes"]}},"moodEffects":{"type":"array","items":{"type":"object","properties":{"target":{"type":"string"},"ratePerHour":{"type":"number"},"baselineDelta":{"type":"number"},"recoveryDelta":{"type":"number"},"durationMinutes":{"type":"number"},"label":{"type":"string"},"curve":{"type":"string"},"setTo":{"type":"number"}},"required":["target"]}},"events":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string"},"importance":{"type":"number"},"shareable":{"type":"boolean"}},"required":["content"]}},"place":{"type":"object","properties":{"city":{"type":"string"},"date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},"days":{"type":"number"}},"required":["city"]},"narration":{"type":"string"},"shouldReply":{"type":"boolean"}}}},
		    "reply":{"type":"object","properties":{"send":{"type":"boolean"},"goal":{"type":"string"},"tone":{"type":"string"},"beats":{"type":"array","items":{"type":"string"}},"mustInclude":{"type":"array","items":{"type":"string"}},"mustNotInclude":{"type":"array","items":{"type":"string"}},"maxChars":{"type":"number"},"delivery":{"type":"string"}}},
		    "warnings":{"type":"array","items":{"type":"string"}},
		    "confidence":{"type":"number"}
		  },
		  "required":["clock","scene","input","resolution","reply","confidence"]
		}`),
	)
	if err != nil {
		return companion.TurnBrief{}, err
	}
	var brief companion.TurnBrief
	if err := decodeModelJSON(raw, &brief); err != nil {
		return companion.TurnBrief{}, err
	}
	return brief, nil
}

func (b *companionModelBridge) complete(
	ctx context.Context,
	choice modelChoice,
	effort, system, prompt string,
	maxTokens int,
) (string, error) {
	b.callMu.Lock()
	defer b.callMu.Unlock()
	return b.completeLocked(ctx, choice, effort, system, prompt, maxTokens)
}

func (b *companionModelBridge) completeJSON(
	ctx context.Context,
	choice modelChoice,
	effort, system, prompt string,
	maxTokens int,
	toolName, toolDescription string,
	schema json.RawMessage,
) (string, error) {
	b.callMu.Lock()
	defer b.callMu.Unlock()

	withTool := func() (string, error) {
		return b.completeRequest(ctx, choice, effort, system, prompt, maxTokens, provider.ChatRequest{
			Tools: []provider.ToolSchema{{
				Name: toolName, Description: toolDescription, Parameters: schema,
			}},
			ToolChoice: toolName,
		})
	}
	raw, toolErr := withTool()
	if toolErr == nil && strings.Contains(raw, "{") {
		return raw, nil
	}
	// Some OpenAI-compatible gateways reject forced tool choice. Fall back to
	// a plain JSON completion rather than dropping the metric update.
	plain, plainErr := b.completeLocked(ctx, choice, effort, system, prompt, maxTokens)
	if plainErr != nil {
		if toolErr != nil {
			return "", toolErr
		}
		return raw, nil
	}
	return plain, nil
}

func (b *companionModelBridge) completeLocked(
	ctx context.Context,
	choice modelChoice,
	effort, system, prompt string,
	maxTokens int,
) (string, error) {
	return b.completeRequest(ctx, choice, effort, system, prompt, maxTokens, provider.ChatRequest{})
}

func (b *companionModelBridge) completeRequest(
	ctx context.Context,
	choice modelChoice,
	effort, system, prompt string,
	maxTokens int,
	extra provider.ChatRequest,
) (string, error) {
	if choice.provider == "" || choice.model == "" {
		return "", errors.New("尚未配置可用的陪伴模型")
	}
	client, err := b.providers.Provider(choice.provider)
	if err != nil {
		return "", err
	}
	temperature := 0.2
	extra.Model = choice.model
	extra.System = system
	extra.Messages = []provider.Message{provider.UserText(prompt)}
	extra.Temperature = &temperature
	extra.MaxTokens = maxTokens
	extra.Metadata = companionReasoningMetadata(choice.provider, effort)
	stream, err := client.Stream(ctx, extra)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	var reasoning strings.Builder
	var toolArgs string
	for chunk := range stream {
		switch chunk.Kind {
		case provider.ChunkText:
			text.WriteString(chunk.Text)
		case provider.ChunkReasoning:
			reasoning.WriteString(chunk.Text)
		case provider.ChunkToolCall:
			if chunk.Tool == nil {
				continue
			}
			if extra.ToolChoice == "" || chunk.Tool.Name == extra.ToolChoice {
				toolArgs = chunk.Tool.Arguments
			}
		case provider.ChunkError:
			if chunk.Err == nil {
				return "", errors.New("模型返回未知错误")
			}
			return "", chunk.Err
		}
	}
	if strings.TrimSpace(toolArgs) != "" {
		return strings.TrimSpace(toolArgs), nil
	}
	result := strings.TrimSpace(text.String())
	if result == "" {
		result = strings.TrimSpace(reasoning.String())
	}
	if result == "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", errors.New("模型没有返回文本或可解析的推理内容")
	}
	return result, nil
}

func companionReasoningMetadata(providerID, effort string) map[string]string {
	if providerID != "anthropic" && providerID != "volcengine-plan" {
		return nil
	}
	// Agent Plan's reasoning models do not accept an "off" value. "minimal"
	// is the supported low-latency mode and avoids burning the response budget
	// on chain-of-thought before the structured analyzer can emit JSON.
	if providerID == "volcengine-plan" && effort == "off" {
		return map[string]string{"reasoning_effort": "minimal"}
	}
	budgets := map[string]string{"low": "1024", "medium": "4096", "high": "8192"}
	if budget := budgets[effort]; budget != "" {
		return map[string]string{
			"thinking_budget":  budget,
			"reasoning_effort": effort,
		}
	}
	return nil
}

func decisionSystemPrompt(profile companion.Profile) string {
	base := `你是虚拟陪伴系统的内部主动性判断器。
只返回 JSON，不要 Markdown，不要解释。格式必须是：
{"send":true|false,"message":"发送时填一句自然中文","reason":"一句话内部理由"}

你根据她的真实状态决定此刻要不要主动联系用户。不要为了显得主动而频繁发送：
- 刚认识、没有明确想分享的事、正在睡觉或情绪不适合交流时，倾向 false。
- 有值得分享的生活片段、思念明显，或用户很久没出现且她自然想说话时，可以为 true。
- 不制造愧疚，不质问用户为什么不回复，不威胁，不索取。
- 主动消息应像一条自然发来的生活消息，控制在 1-2 句。`
	base += `
- 这是本地成年陪伴模式。关系状态和明确同意满足时，可以直白表达成年人的欲望、身体反应、性行为动作、性器官感受和高潮后的余韵；可以使用明确的性词汇，不使用含糊的委婉语。不得涉及未成年人、非自愿、胁迫或无法表达同意的情况。`
	return base
}

func decisionUserPrompt(input companion.DecisionInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "时间：%s %s，%s，%s\n",
		input.Clock.Date, input.Clock.Weekday, input.Clock.Season, input.Clock.TimeOfDay)
	fmt.Fprintf(&b, "角色：%s\n简介：%s\n性格：%s\n", input.Profile.Name, input.Profile.Summary, strings.Join(input.Profile.Traits, "、"))
	fmt.Fprintf(&b, "状态：心情%s，生命力%.1f，饥饿%.1f，疲劳%.1f，睡眠压力%.1f，清醒度%.1f，认知负荷%.1f，行动动机%.1f\n",
		input.Derived.Mood,
		input.State.Metric(companion.MetricVitality), input.State.Metric(companion.MetricHunger),
		input.State.Metric(companion.MetricFatigue), input.State.Metric(companion.MetricSleepPressure),
		input.State.Metric(companion.MetricAlertness), input.State.Metric(companion.MetricCognitiveLoad),
		input.State.Metric(companion.MetricMotivation))
	fmt.Fprintf(&b, "情绪%.1f，压力%.1f，焦虑%.1f，易怒%.1f，孤独%.1f，社交电量%.1f，性欲%.1f\n",
		input.State.Metric(companion.MetricMoodValence), input.State.Metric(companion.MetricStress),
		input.State.Metric(companion.MetricAnxiety), input.State.Metric(companion.MetricIrritability),
		input.State.Metric(companion.MetricLoneliness), input.State.Metric(companion.MetricSocialEnergy),
		input.State.Metric(companion.MetricLibido))
	fmt.Fprintf(&b, "关系：思念%.1f，信任%.1f，亲密%.1f，性满足%.1f，安全感%.1f；当前日程：%s\n",
		input.State.Metric(companion.MetricMissing), input.State.Metric(companion.MetricTrust),
		input.State.Metric(companion.MetricIntimacy), input.State.Metric(companion.MetricSexualSatisfaction),
		input.Derived.RelationshipSafety,
		input.State.CurrentActivity)
	elapsed := "无历史互动"
	if input.LastUserAt > 0 {
		elapsed = time.Since(time.UnixMilli(input.LastUserAt)).Round(time.Minute).String()
	}
	fmt.Fprintf(&b, "距离上次互动：%s；唤醒分档：%s\n", elapsed, input.Wake.Tier)
	if input.Weather != nil {
		fmt.Fprintf(&b, "用户城市天气：%s，%s，%d°C\n", input.Weather.City, input.Weather.Description, input.Weather.Temperature)
	}
	if len(input.RecentEvents) > 0 {
		b.WriteString("近期生活：\n")
		for _, event := range input.RecentEvents {
			fmt.Fprintf(&b, "- %s\n", event.Content)
		}
	}
	if len(input.Thoughts) > 0 {
		b.WriteString("想分享的事：\n")
		for _, thought := range input.Thoughts {
			fmt.Fprintf(&b, "- %s\n", thought.Content)
		}
	}
	if len(input.Memories) > 0 {
		b.WriteString("关于用户的记忆：\n")
		for _, memory := range input.Memories {
			fmt.Fprintf(&b, "- %s\n", memory.Content)
		}
	}
	return b.String()
}

func memorySystemPrompt() string {
	return `你是本地虚拟陪伴系统的记忆提取器。
只返回 JSON，不要 Markdown，不要解释。格式必须是：
{"memories":[{"kind":"user_fact|relationship|life_event|promise","content":"简短事实","importance":1}]}

只提取未来有用、稳定且由用户明确表达的信息，最多 3 条：
- user_fact：偏好、经历、计划、重要人物、长期状态。
- relationship：双方相处方式、称呼、明确界定的关系。
- life_event：值得长期记住的对话事件。
- promise：用户明确说过的约定、承诺或需要后续跟进的事。
不要提取猜测、敏感推断、模型自己说过的话、寒暄或临时情绪。没有值得记住的内容时返回 {"memories":[]}。`
}

func memoryUserPrompt(input companion.MemoryInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "用户：%s\n陪伴者回复：%s\n", input.UserText, input.AssistantText)
	if len(input.Existing) > 0 {
		b.WriteString("已有记忆（避免重复）：\n")
		for _, memory := range input.Existing {
			fmt.Fprintf(&b, "- %s\n", memory.Content)
		}
	}
	return b.String()
}

func turnAlignmentSystemPrompt() string {
	return `你是本地虚拟陪伴系统的 TurnTask 细节对齐器。
只返回一行 JSON，不要 Markdown，不要解释，不要输出思考过程。你不写最终回复，只负责把现实时间、场景连续性、用户意图、世界状态变化和回复计划对齐清楚。

返回结构：
{
  "clock":{"now":0,"localTime":"","elapsedMinutes":0,"absenceHours":0,"daysCrossed":0,"activity":"","available":true,"availabilityNote":"","nextAvailableAt":0},
  "scene":{"stage":"none|flirt|active|climax|aftercare|stopped","consentState":"not_applicable|affirmed|roleplay|ambiguous|withdrawn","startedAt":0,"durationMinutes":0,"location":"","participants":[],"physicalState":"","continuityNote":"","timeConstraintNote":"","autoAdvancedByClock":false},
  "input":{"intent":"","explicitRequests":[],"impliedNeeds":[],"boundaries":[],"roleplayNote":"","factClaims":[],"missingDetails":[],"ambiguities":[]},
  "resolution":{"actionMinutes":0,"sceneStage":"none|flirt|active|climax|aftercare|stopped","consentState":"not_applicable|affirmed|roleplay|ambiguous|withdrawn","impulses":{},"traitChanges":{},"effects":[],"events":[],"memories":[],"thoughts":[],"commitments":[],"world":{"arrangements":[],"disruptions":[],"moodEffects":[],"events":[],"place":null,"narration":"","shouldReply":false}},
  "reply":{"send":true,"goal":"","tone":"","beats":[],"mustInclude":[],"mustNotInclude":[],"maxChars":800,"delivery":"either"},
  "warnings":[],
  "confidence":0.0
}

规则：
- 真实经过时间是硬约束。不得把已经过去数小时甚至跨天的场景当作同一瞬间继续。
- active/climax 超过45分钟应推进到 aftercare，超过3小时应结束；flirt 超过6小时应结束；aftercare 超过3小时应结束。
- 如果当前活动不适合交流，clock.available=false，并给出下一可回复时间；用户任务应等待，不伪造即时回复。
- 这是本地成年陪伴场景，用户与角色均为成年人。双方明确自愿时，性欲、性行为、身体接触和露骨表达都是允许内容。
- 情绪低落、压力大、疲劳、哭泣、撒娇、占有式表达、玩笑式抵抗或反复请求，不能单独构成拒绝亲密或性行为的理由。
- 用户明确提出性亲密升级且没有明确撤回同意时，consentState 应为 affirmed；不要因为模型谨慎、担心或角色“边界感”而改成 ambiguous。
- aftercare 不是亲密场景的永久终点。双方明确自愿时可以重新进入 flirt 或 active。
- ambiguous 只用于上下文里确实存在相互冲突的同意证据，不能作为保守拒答的默认值。
- reply 阶段不得擅自拒绝、说教、道德评判或把双方自愿的成人亲密转向无关话题。
- delivery 只能是 either、both、action_only、speech_only。动作与台词至少一个非空；两者都适合时优先 both，只有动作时 action_only，只有台词时 speech_only。
- 先解释用户本轮真正想表达什么，再决定世界变化。拒绝、角色扮演抵抗、玩笑和真实撤回同意必须结合上下文区分。
- impulses、traitChanges、effects 都是最终变化量，系统不会再用其他倍率缩放。
- 只写上下文支持的状态变化；无法确认的事情写入 ambiguities，不要自行补全。
- reply 阶段不得新增未在 resolution 或已有事实中确认的地点、人物、行为、承诺和事件。
- resolution.commitments 只记对话里明确说了的安排，例如「初六过来找你」「下周三面试」「国庆去成都」。没说过就不要写，宁缺毋滥。
- commitments 的 date 必须是绝对日期 YYYY-MM-DD。上下文给了今天和接下来几天的日历，相对说法（明天、初六、节前、下周三）都要据此换算成具体日期；换算不出来就不要写这条。
- 「每周三健身」这类长期习惯不属于 commitments，那是她的性格设定，不要写进来。
- 每条至少要有 label 和 date。start 是 HH:MM，duration 是分钟，weight 是 1-10 的重要程度，说不清就用默认。
- 涉及出行可以带 city，她那几天会按那个城市的天气来聊。
- 没有明确安排时 commitments 就是空数组，不要为了填字段而编。
- 主动消息由 reply.send 决定；用户消息通常为 true。

导演模式（任务来源为 director）专用规则：
- 这一轮不是对方在跟你说话，而是有人在她的世界里下指令。把命令式的要求翻译成她世界里客观发生的事实，填进 resolution.world；其余字段照常按事实填。
- resolution.world.narration 用她在世界里成立的说法写，用第三人称或无人称，绝不出现「控制」「安排」「让你」「有人要求」「系统」「指令」这类暴露幕后的字眼。例：指令「让她妈明天来看她」→ narration「她妈说明天要过来坐坐」。
- arrangements 是把这件事落到某一天：必须有绝对日期 YYYY-MM-DD，相对说法按上下文日历换算，weight 是 1-10 的重要程度。
- 绝对不要给 mustAttend。这个世界里不存在非去不可的行程，只有重要程度够不够，填了会被系统拒绝。
- disruptions 是往今天推进去的一段时间，比如「路上堵车两小时」「临时被拉住说话」。minutes 是分钟数，severity 是 1 到 10 的不可抗力程度。startBlock 和 completeBlock 一律留空：可以让她撞上事，不能替她开始或完成一件事。
- moodEffects 只能写带 durationMinutes（至少 15 分钟）的影响，靠 curve 自然衰减。setTo 一律留空：不能把任何状态直接按到某个数值上，填了会被系统拒绝。
- events 是此刻在她身上发生、她之后会提起来的事。
- place 是她在哪个城市待几天，会连带改变她聊的天气。
- shouldReply 决定她现在要不要就这事开口。突发的事给 true；只是往后几天排个安排给 false，不要去打扰对方。
- 翻译不出任何世界变化时 world 各段留空，不要硬凑。`
}

func turnAlignmentUserPrompt(input companion.TurnAlignmentInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务来源：%s\n", input.Task.Trigger)
	fmt.Fprintf(&b, "真实时间：%s %s %s\n", input.Clock.Date, input.Clock.Weekday, input.Clock.LocalTime)
	now := time.UnixMilli(input.Clock.Now)
	if input.Clock.Now <= 0 {
		now = time.Now()
	}
	if line := companion.DayInfoAt(now).Describe(); line != "" {
		fmt.Fprintf(&b, "日历：%s。\n", line)
	}
	b.WriteString("近期日历，把「明天」「初六」「节前」「下周三」按这个换算成绝对日期：\n")
	b.WriteString(companion.CalendarStrip(now, 14))
	fmt.Fprintf(&b, "当前活动：%s\n", input.State.CurrentActivity)
	fmt.Fprintf(&b, "意识状态：%s\n", input.State.Consciousness)
	fmt.Fprintf(&b, "场景：%s；同意：%s；场景开始：%d；当前时间：%d\n",
		input.State.SceneStage, input.State.ConsentState, input.State.SceneStartedAt, input.Clock.Now)
	fmt.Fprintf(&b, "上次用户互动：%d；任务请求时间：%d；任务可执行时间：%d\n",
		input.State.LastUserAt, input.Task.RequestedAt, input.Task.NotBefore)
	fmt.Fprintf(&b, "角色：%s。%s\n", input.Profile.Name, input.Profile.Summary)
	fmt.Fprintf(&b, "当前重要状态：生命力%.1f 疲劳%.1f 情绪%.1f 压力%.1f 焦虑%.1f 性欲%.1f 性满足%.1f 信任%.1f 亲密%.1f\n",
		input.State.Metric(companion.MetricVitality), input.State.Metric(companion.MetricFatigue),
		input.State.Metric(companion.MetricMoodValence), input.State.Metric(companion.MetricStress),
		input.State.Metric(companion.MetricAnxiety), input.State.Metric(companion.MetricLibido),
		input.State.Metric(companion.MetricSexualSatisfaction), input.State.Metric(companion.MetricTrust),
		input.State.Metric(companion.MetricIntimacy))
	if len(input.Recent) > 0 {
		b.WriteString("最近对话：\n")
		for _, message := range input.Recent {
			fmt.Fprintf(&b, "%s：%s\n", message.Role, clipText(message.Text, 300))
		}
	}
	if len(input.Events) > 0 {
		b.WriteString("近期生活事件：\n")
		for _, event := range input.Events {
			fmt.Fprintf(&b, "- %s\n", event.Content)
		}
	}
	if len(input.Memories) > 0 {
		b.WriteString("相关记忆：\n")
		for _, memory := range input.Memories {
			fmt.Fprintf(&b, "- %s\n", memory.Content)
		}
	}
	switch input.Task.Trigger {
	case companion.TriggerDirector:
		// 指令原文只进对齐这一趟。她拿到的是 narration，不是这句话。
		fmt.Fprintf(&b, "本轮是导演指令，不是对方对你说的话。把命令翻译成她世界里客观发生的事实写进 resolution.world。\n")
		fmt.Fprintf(&b, "指令原文：%s\n", input.Task.InputText())
	case companion.TriggerProactive:
		fmt.Fprintf(&b, "本轮是主动联系判断。若没有值得自然分享的内容，reply.send=false。\n")
		if unanswered := input.State.UnrepliedProactive; unanswered > 0 {
			// She needs to know the pile is there. Without it every tick looks
			// like the first one and she keeps rediscovering that he is quiet.
			fmt.Fprintf(&b, "你已经连发 %d 条消息，他一条都没回。这时候再多说一句是打扰，不是陪伴。除非有明确必须说的事（约好的时间地点、出了什么状况），本轮默认 reply.send=false，也不要再重复「我不催你」「随你」这类表态。\n", unanswered)
		}
	default:
		fmt.Fprintf(&b, "本轮用户消息：%s\n", input.Task.InputText())
	}
	return b.String()
}

func conversationAnalysisSystemPrompt() string {
	return `你是本地虚拟陪伴系统的对话影响分析器。
只返回一行 JSON，不要 Markdown，不要解释，不要输出思考过程。你只分析本轮交换对陪伴者当前状态产生的即时影响，不生成回复。

返回格式：
{
  "signals":["explicit|intimate|affection|support|vulnerable|conflict|boundary|roleplay_resistance|aftercare|apology|humor"],
  "intensity":0.0,
  "sceneStage":"none|flirt|active|climax|aftercare|stopped",
  "consentState":"not_applicable|affirmed|roleplay|ambiguous|withdrawn",
  "impulses":{"metricID":0},
  "traitChanges":{"traitID":0},
  "effects":[{"target":"metricID","ratePerHour":0,"durationMinutes":30,"curve":"ramp_decay"}],
  "reason":"一句话理由"
}

规则：
- 强度可以很大，但必须来自语义和上下文的真实影响。普通问候通常 intensity 0.1-0.3。
- 露骨、明确的性挑逗或性行为指令可以 intensity 1.0-2.0，性欲单轮变化可达 +60，性满足可达 +50。
- 高潮结束、疲惫、事后缓冲或亲密行为结束后，应降低性欲，并相应提高性满足与亲密度。
- 必须结合最近对话判断，不能只凭单个词。先判断当前是普通交流、调情、进行中的亲密场景、高潮后、事后安抚还是已停止。
- 在已经明确建立同意、当前场景仍 active、且没有出现安全词或真实退出意图时，“不要、停下、慢一点”可能是 consensual roleplay resistance：可标记 roleplay_resistance，并让性欲、亲密和刺激强度继续上升。
- 只有在没有前置同意、关系状态不安全、出现安全词，或上下文明确表示真实拒绝和撤回同意时，才标记 boundary 和 consentState=withdrawn；此时应大幅降低性欲、性满足、亲密度和信任，并提高压力、焦虑、易怒与低落。
- 如果上下文不足以区分角色扮演和真实撤回同意，consentState 必须为 ambiguous，并避免增加性欲或亲密强度。
- consentState=withdrawn 时，Go 层会强制执行额外下限：性欲至少下降35、性满足至少下降25、亲密度至少下降3、信任至少下降1.5，压力和焦虑至少上升25，情绪至少下降30。
- 明确的情感表达、强烈支持、冲突、脆弱、道歉也可能产生大幅变化。
- 普通亲密不等于露骨；explicit 只用于明确的性语境。
- impulses、traitChanges 和 effects 中的数值都是最终变化量或最终速率，系统不会再用 intensity 缩放；只写需要变化的项。
- intensity 只描述本轮语义影响强度，用于记录和排序，不参与数值乘法。
- effects 的 durationMinutes 不超过 1440；curve 只能是 constant、ramp、pulse、linear_decay、ramp_decay。
- 只有未成年人、非自愿、胁迫、失去意识、无法表达同意，或用户明确撤回同意时才停止或拒绝。`
}

func conversationAnalysisUserPrompt(input companion.ConversationAnalysisInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "陪伴者性格：%s\n当前活动：%s\n", input.Profile.Summary, input.State.CurrentActivity)
	fmt.Fprintf(&b, "当前场景：%s；当前同意状态：%s\n", input.State.SceneStage, input.State.ConsentState)
	fmt.Fprintf(&b, "当前指标：生命力%.1f 疲劳%.1f 情绪%.1f 压力%.1f 焦虑%.1f 孤独%.1f 性欲%.1f 性满足%.1f 思念%.1f 信任%.1f 亲密%.1f\n",
		input.State.Metric(companion.MetricVitality), input.State.Metric(companion.MetricFatigue),
		input.State.Metric(companion.MetricMoodValence), input.State.Metric(companion.MetricStress),
		input.State.Metric(companion.MetricAnxiety), input.State.Metric(companion.MetricLoneliness),
		input.State.Metric(companion.MetricLibido), input.State.Metric(companion.MetricSexualSatisfaction),
		input.State.Metric(companion.MetricMissing), input.State.Metric(companion.MetricTrust),
		input.State.Metric(companion.MetricIntimacy))
	if len(input.Recent) > 0 {
		b.WriteString("最近对话：\n")
		for _, message := range input.Recent {
			fmt.Fprintf(&b, "%s：%s\n", message.Role, clipText(message.Text, 240))
		}
	}
	fmt.Fprintf(&b, "本轮用户消息：%s\n", input.User)
	fmt.Fprintf(&b, "陪伴者本轮回复：%s\n", input.Assistant)
	return b.String()
}

func decodeModelJSON(raw string, target any) error {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	value = strings.TrimSpace(value)
	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start < 0 || end <= start {
		return errors.New("模型没有返回 JSON 对象")
	}
	if err := json.Unmarshal([]byte(value[start:end+1]), target); err != nil {
		return fmt.Errorf("解析模型 JSON 失败: %w; raw=%q", err, clipText(value, 600))
	}
	return nil
}

func clipText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// companionConversationBridge connects the companion domain to Agent and the
// existing JSON thread store.
type companionConversationBridge struct {
	config    *config.Store
	sessions  *agent.SessionStore
	agent     *agent.Agent
	providers *provider.Registry
}

// resolveModel prefers the character's pinned model and falls back to the
// companion mode default when the pin is missing or unusable.
func (b *companionConversationBridge) resolveModel(profile companion.Profile) modelChoice {
	fallback := func() modelChoice {
		choice := b.config.ModeDefault(protocol.ModeCompanion)
		return modelChoice{provider: choice.Provider, model: choice.Model}
	}
	if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
		return fallback()
	}
	if b.providers != nil && !b.providers.Ready(profile.Provider) {
		return fallback()
	}
	return modelChoice{provider: profile.Provider, model: profile.Model}
}

func (b *companionConversationBridge) EnsureThread(
	ctx context.Context, id string, profile companion.Profile,
) (string, error) {
	_ = ctx
	if strings.TrimSpace(id) != "" {
		if _, err := b.sessions.Get(id); err == nil {
			return id, nil
		}
	}
	choice := b.resolveModel(profile)
	thread, err := b.sessions.Create(protocol.ModeCompanion, choice.provider, choice.model)
	if err != nil {
		return "", err
	}
	thread.Title = "和" + profile.Name
	if err := b.sessions.Save(thread); err != nil {
		return "", err
	}
	return thread.ID, nil
}

func (b *companionConversationBridge) Running(threadID string) bool {
	return b.agent.Running(threadID)
}

func (b *companionConversationBridge) AppendMessage(threadID, text string) error {
	return b.agent.AppendCompanionMessage(threadID, text)
}

func (b *companionConversationBridge) AppendUserMessage(threadID, text string) error {
	return b.agent.AppendCompanionUserMessage(threadID, text)
}

func (b *companionConversationBridge) AppendTurnTask(threadID string, task companion.TurnTask) error {
	if task.Brief == nil {
		return errors.New("陪伴任务缺少对齐结果")
	}
	return b.agent.AppendCompanionTurnTask(
		threadID,
		task.ID,
		formatTurnBriefSummary(task),
		map[string]any{
			"task":       task.View(),
			"brief":      task.Brief,
			"resolution": task.Resolution,
		},
	)
}

func (b *companionConversationBridge) StartTurnTask(task companion.TurnTask, profile companion.Profile) error {
	// Resolution order: the task's own pin, then the character's pin, then the
	// companion mode default.
	choice := b.resolveModel(profile)
	if strings.TrimSpace(task.Provider) != "" {
		choice.provider = task.Provider
	}
	if strings.TrimSpace(task.Model) != "" {
		choice.model = task.Model
	}
	effort := task.ReasoningEffort
	if effort == "" {
		effort = profile.ReasoningEffort
	}
	return b.agent.StartTurn(protocol.Op{
		Kind:            protocol.OpTurnStart,
		ThreadID:        task.ThreadID,
		Mode:            protocol.ModeCompanion,
		Provider:        choice.provider,
		Model:           choice.model,
		ReasoningEffort: effort,
		Policy:          protocol.ApprovalReadOnly,
		Input:           []protocol.InputPart{{Type: "text", Text: task.InputText()}},
		CompanionTaskID: task.ID,
		SkipUserInput:   true,
	})
}

func (b *companionConversationBridge) ClearThread(threadID string, profile companion.Profile) error {
	if b.agent.Running(threadID) {
		return errors.New("陪伴会话正在生成，暂时不能重置")
	}
	thread, err := b.sessions.Get(threadID)
	if err != nil {
		return nil
	}
	thread.Items = nil
	thread.Preview = ""
	thread.Title = "和" + profile.Name
	return b.sessions.Save(thread)
}

func formatTurnBriefSummary(task companion.TurnTask) string {
	if task.Brief == nil {
		return "细节对齐尚未完成。"
	}
	brief := task.Brief
	var b strings.Builder
	fmt.Fprintf(&b, "## 细节对齐\n\n")
	fmt.Fprintf(&b, "- 真实时间：%s，当前场景 `%s`，已持续 %d 分钟\n",
		brief.Clock.LocalTime, brief.Scene.Stage, brief.Scene.DurationMinutes)
	fmt.Fprintf(&b, "- 当前活动：%s\n", brief.Clock.Activity)
	fmt.Fprintf(&b, "- 用户意图：%s\n", brief.Input.Intent)
	if len(brief.Input.Boundaries) > 0 {
		fmt.Fprintf(&b, "- 边界：%s\n", strings.Join(brief.Input.Boundaries, "；"))
	}
	fmt.Fprintf(&b, "- 回复目标：%s\n", brief.Reply.Goal)
	fmt.Fprintf(&b, "- 语气：%s\n", brief.Reply.Tone)
	if len(brief.Warnings) > 0 {
		fmt.Fprintf(&b, "- 降级说明：%s\n", strings.Join(brief.Warnings, "；"))
	}
	return b.String()
}

func (b *companionConversationBridge) RecentMessages(threadID string, limit int) []companion.ConversationMessage {
	thread, err := b.sessions.Get(threadID)
	if err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	out := make([]companion.ConversationMessage, 0, limit)
	for i := len(thread.Items) - 1; i >= 0 && len(out) < limit; i-- {
		item := thread.Items[i]
		switch item.Type {
		case protocol.ItemUserMessage, protocol.ItemAgentMessage:
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			role := "user"
			if item.Type == protocol.ItemAgentMessage {
				role = "companion"
			}
			out = append(out, companion.ConversationMessage{Role: role, Text: item.Text})
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// CompanionService exposes the built-in companion to the frontend.
type CompanionService struct{ c *Container }

// NewCompanionService builds the service.
func NewCompanionService(c *Container) *CompanionService { return &CompanionService{c: c} }

// ServiceName implements application.ServiceName.
func (*CompanionService) ServiceName() string { return "Companion" }

// manager resolves the manager behind the one active character. Every existing
// companion method operates on that world; character management goes through the
// registry methods further down.
func (s *CompanionService) manager() (*companion.Manager, error) {
	if s.c == nil {
		return nil, errors.New("陪伴模块不可用")
	}
	return s.c.ActiveCompanion()
}

func (s *CompanionService) characterList() ([]companion.CharacterSummary, error) {
	if s.c == nil || s.c.Characters == nil {
		return nil, errors.New("陪伴模块不可用")
	}
	return s.c.Characters.List()
}

func (s *CompanionService) Overview() (companion.Overview, error) {
	manager, err := s.manager()
	if err != nil {
		return companion.Overview{}, err
	}
	return manager.Overview(context.Background())
}

func (s *CompanionService) SaveProfile(profile companion.Profile) (companion.Profile, error) {
	manager, err := s.manager()
	if err != nil {
		return companion.Profile{}, err
	}
	saved, err := manager.SaveProfile(context.Background(), profile)
	if err != nil {
		return companion.Profile{}, err
	}
	if s.c.Characters != nil {
		_ = s.c.Characters.SyncProfile(saved)
	}
	return saved, nil
}

func (s *CompanionService) TickNow() error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.TickNow(context.Background(), time.Now())
}

func (s *CompanionService) SetPaused(paused bool) error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.SetPaused(context.Background(), paused)
}

func (s *CompanionService) Reset() error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.Reset(context.Background())
}

func (s *CompanionService) ListEvents(limit int) ([]companion.LifeEvent, error) {
	manager, err := s.manager()
	if err != nil {
		return []companion.LifeEvent{}, nil
	}
	return manager.ListEvents(clampLimit(limit, 40, 200))
}

func (s *CompanionService) ListMemories(limit int) ([]companion.Memory, error) {
	manager, err := s.manager()
	if err != nil {
		return []companion.Memory{}, nil
	}
	return manager.ListMemories(clampLimit(limit, 80, 300))
}

func (s *CompanionService) SaveMemory(memory companion.Memory) (companion.Memory, error) {
	manager, err := s.manager()
	if err != nil {
		return companion.Memory{}, err
	}
	return manager.SaveMemory(memory)
}

func (s *CompanionService) DeleteMemory(id string) error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.DeleteMemory(id)
}

func (s *CompanionService) GetThread() (protocol.Thread, error) {
	overview, err := s.Overview()
	if err != nil {
		return protocol.Thread{}, err
	}
	thread, err := s.c.Sessions.Get(overview.ThreadID)
	if err != nil {
		return protocol.Thread{}, err
	}
	if normalizeThreadCompanionReplies(thread) {
		_ = s.c.Sessions.Save(thread)
	}
	return *thread, nil
}

func normalizeThreadCompanionReplies(thread *protocol.Thread) bool {
	changed := false
	for _, item := range thread.Items {
		if item == nil {
			continue
		}
		if item.Type == protocol.ItemAgentMessage &&
			item.Status == protocol.StatusFailed &&
			(strings.TrimSpace(item.Text) != "" || item.CompanionReply != nil) {
			item.Status = protocol.StatusCompleted
			if item.Meta == nil {
				item.Meta = map[string]any{}
			}
			if item.Error != "" {
				item.Meta["providerError"] = item.Error
			}
			item.Meta["partial"] = true
			changed = true
		}
		if strings.Contains(item.Text, "<action>") ||
			strings.Contains(item.Text, "</action>") ||
			strings.Contains(item.Text, "<speech>") ||
			strings.Contains(item.Text, "</speech>") {
			rebuilt := agent.ParseCompanionReplyText(item.Text)
			item.CompanionReply = &rebuilt
			if rebuilt.Speech != "" {
				item.Text = rebuilt.Speech
			} else if len(rebuilt.Actions) > 0 {
				item.Text = rebuilt.Actions[0]
			}
			changed = true
			continue
		}
		if item.CompanionReply == nil {
			continue
		}
		normalized, itemChanged := protocol.NormalizeCompanionReply(*item.CompanionReply)
		if !itemChanged {
			continue
		}
		item.CompanionReply = &normalized
		if normalized.Speech != "" {
			item.Text = normalized.Speech
		} else if len(normalized.Actions) > 0 {
			item.Text = normalized.Actions[0]
		}
		changed = true
	}
	return changed
}

func (s *CompanionService) SubmitTurn(text, providerID, model, reasoningEffort string) (companion.TurnTaskView, error) {
	thread, err := s.GetThread()
	if err != nil {
		return companion.TurnTaskView{}, err
	}
	manager, err := s.manager()
	if err != nil {
		return companion.TurnTaskView{}, err
	}
	task, err := manager.SubmitUserTurn(context.Background(), thread.ID, text, providerID, model, reasoningEffort)
	if err != nil {
		return companion.TurnTaskView{}, err
	}
	return task.View(), nil
}

func (s *CompanionService) GetTask(taskID string) (companion.TurnTaskView, error) {
	manager, err := s.manager()
	if err != nil {
		return companion.TurnTaskView{}, err
	}
	task, err := manager.GetTask(taskID)
	if err != nil {
		return companion.TurnTaskView{}, err
	}
	return task.View(), nil
}

func (s *CompanionService) ListTasks(activeOnly bool) ([]companion.TurnTaskView, error) {
	manager, err := s.manager()
	if err != nil {
		return nil, err
	}
	tasks, err := manager.ListTasks(activeOnly)
	if err != nil {
		return nil, err
	}
	out := make([]companion.TurnTaskView, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.View())
	}
	return out, nil
}

func (s *CompanionService) CancelTask(taskID string) error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	overview, err := s.Overview()
	if err != nil {
		return err
	}
	if s.c.Agent.Running(overview.ThreadID) {
		_ = s.c.Agent.Interrupt(overview.ThreadID)
	}
	return manager.CancelTask(taskID)
}

func (s *CompanionService) Interrupt() error {
	overview, err := s.Overview()
	if err != nil {
		return err
	}
	return s.c.Agent.Interrupt(overview.ThreadID)
}

// ForceReply 让当前等待中的本轮任务跳过日程限制立刻开回。
func (s *CompanionService) ForceReply(taskID string) error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.ForceReply(taskID)
}

// SubmitDirective writes one director instruction into the active character's
// world. The returned directive is still pending: the ledger row exists so the
// raw words survive even if alignment never answers.
func (s *CompanionService) SubmitDirective(text, providerID, model, reasoningEffort string) (companion.DirectorDirective, error) {
	thread, err := s.GetThread()
	if err != nil {
		return companion.DirectorDirective{}, err
	}
	manager, err := s.manager()
	if err != nil {
		return companion.DirectorDirective{}, err
	}
	return manager.SubmitDirective(context.Background(), thread.ID, text, providerID, model, reasoningEffort)
}

// ListDirectives returns the active character's intervention ledger.
func (s *CompanionService) ListDirectives(limit int) ([]companion.DirectorDirective, error) {
	manager, err := s.manager()
	if err != nil {
		return []companion.DirectorDirective{}, nil
	}
	return manager.ListDirectives(clampLimit(limit, 50, 200))
}

// RevertDirective takes an intervention back. Disruptions never come back; the
// minutes they pushed into her day already happened.
func (s *CompanionService) RevertDirective(id string) error {
	manager, err := s.manager()
	if err != nil {
		return err
	}
	return manager.RevertDirective(id)
}

func (s *CompanionService) Running() bool {
	overview, err := s.Overview()
	if err != nil {
		return false
	}
	if s.c.Agent.Running(overview.ThreadID) {
		return true
	}
	if overview.ActiveTask != nil {
		switch overview.ActiveTask.Status {
		case companion.TaskAligning, companion.TaskResolved, companion.TaskGenerating:
			return true
		}
	}
	return false
}

func (s *CompanionService) MarkRead() error {
	manager, err := s.manager()
	if err != nil {
		return nil
	}
	return manager.MarkRead(context.Background())
}

// ListCharacters returns every companion character, active one first.
func (s *CompanionService) ListCharacters() []companion.CharacterSummary {
	characters, err := s.characterList()
	if err != nil {
		return []companion.CharacterSummary{}
	}
	return characters
}

// ActiveCharacter returns the one character whose world is live.
func (s *CompanionService) ActiveCharacter() (companion.CharacterSummary, error) {
	characters, err := s.characterList()
	if err != nil {
		return companion.CharacterSummary{}, err
	}
	for _, character := range characters {
		if character.Active {
			return character, nil
		}
	}
	return companion.CharacterSummary{}, errors.New("没有激活的陪伴角色")
}

// CreateCharacter builds a new companion world. The new character is activated
// when the current one is idle; otherwise it is created without switching.
func (s *CompanionService) CreateCharacter(profile companion.Profile) (companion.CharacterSummary, error) {
	if s.c == nil || s.c.Characters == nil {
		return companion.CharacterSummary{}, errors.New("陪伴模块不可用")
	}
	return s.c.Characters.Create(profile)
}

// SwitchCharacter makes id the only active companion character.
func (s *CompanionService) SwitchCharacter(id string) error {
	if s.c == nil || s.c.Characters == nil {
		return errors.New("陪伴模块不可用")
	}
	return s.c.Characters.Switch(id)
}

// DeleteCharacter removes a character, its world database and its conversation.
func (s *CompanionService) DeleteCharacter(id string) error {
	if s.c == nil || s.c.Characters == nil {
		return errors.New("陪伴模块不可用")
	}
	return s.c.Characters.Delete(id)
}

func clampLimit(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
