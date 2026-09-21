// 对话后端调用(会话、轮次、审批)。
import { Chat } from "./bindings";
import { list } from "./helpers";
import type { ConversationSummary } from "../types/chat";
import type { ApprovalPolicy, Thread, ThreadSummary } from "./bindings";
import { OpKind } from "./bindings";
import type { Attachment } from "./bindings";

/** 把后端会话摘要转成 UI 用的窄类型。 */
function toSummary(summary: ThreadSummary): ConversationSummary {
  return {
    id: summary.id,
    title: summary.title || "未命名对话",
    mode: summary.mode ?? "work",
    pinned: summary.pinned ?? false,
    updatedAt: summary.updatedAt ?? summary.createdAt ?? 0,
    messageCount: summary.itemCount ?? 0,
  };
}

export const chatRepository = {
  listConversations: async (): Promise<ConversationSummary[]> =>
    list(await Chat.ListThreads()).map(toSummary),

  createConversation: (mode: string): Promise<Thread> => Chat.CreateThread(mode),

  getConversation: (id: string): Promise<Thread> => Chat.GetThread(id),

  deleteConversation: (id: string): Promise<void> => Chat.DeleteThread(id),

  renameConversation: (id: string, title: string): Promise<void> => Chat.RenameThread(id, title),

  pinConversation: (id: string, pinned: boolean): Promise<void> => Chat.PinThread(id, pinned),

  clearConversation: (id: string): Promise<void> => Chat.ClearThreadItems(id),

  isRunning: (id: string): Promise<boolean> => Chat.Running(id),

  /** sendTurn 提交一轮对话。 */
  sendTurn: (input: {
    threadId: string;
    text: string;
    mode: string;
    provider: string;
    model: string;
    collections?: string[];
    policy: ApprovalPolicy | string;
	attachments?: Attachment[];
	reasoningEffort?: string;
  }): Promise<void> =>
    Chat.SubmitTurn({
      kind: OpKind.OpTurnStart,
      threadId: input.threadId,
      mode: input.mode as never,
      policy: input.policy as never,
      provider: input.provider,
      model: input.model,
		collections: input.collections ?? [],
      attachments: input.attachments ?? [],
		reasoningEffort: input.reasoningEffort ?? "off",
      input: [{ type: "text", text: input.text }],
    }),

  interrupt: (threadId: string): Promise<void> => Chat.Interrupt(threadId),

  respondApproval: (
    threadId: string,
    approvalId: string,
    approve: boolean,
    remember: boolean,
  ): Promise<void> => Chat.RespondApproval(threadId, approvalId, approve, remember),

  updateConversationSettings: (
    threadId: string,
    provider: string,
    model: string,
    mode: string,
  ): Promise<void> => Chat.UpdateThreadSettings(threadId, provider, model, mode),

  exportConversation: (threadId: string, saveToFile = true): Promise<string> =>
    Chat.ExportThread(threadId, saveToFile),
};
