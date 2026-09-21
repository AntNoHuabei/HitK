// 对话领域的 UI 类型。
//
// 消息与工具卡片直接渲染后端协议模型(Item/Event),这里只放对话列表与
// 上下文用量这类视图专用的类型。
export type AgentMode = "work" | "coding";

export type ApprovalPolicy = "auto" | "readOnly" | "manual";

export interface ConversationSummary {
  id: string;
  title: string;
  mode: string;
  pinned: boolean;
  updatedAt: number;
  messageCount: number;
}

export interface ContextUsage {
  usedTokens: number;
  contextWindow: number;
}

/** 一轮对话结束后 UI 展示的统计。 */
export interface TurnStats {
  model: string;
  inputTokens: number;
  outputTokens: number;
  elapsedMs: number;
}
