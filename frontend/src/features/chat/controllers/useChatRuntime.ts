// 对话运行时:把后端事件流、会话列表与发送/中断动作接到 store 上。
//
// 组件只跟这个 hook 打交道,store 只被这里写入,事件订阅也只有这里一份。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { chatRepository, errorText } from "../../../shared/repositories";
import { onProtocolEvent } from "../../../shared/wails/events";
import type { ConversationSummary } from "../../../shared/types/chat";
import type { Attachment } from "../../../shared/repositories";
import { useConversationStore } from "../model/conversationStore";

interface RuntimeOptions {
  /** 发送一轮时使用的前缀,区分不同来源(当前只有主对话)。 */
  promptPrefix?: string;
  /** 一轮结束后回调,用来刷新会话列表标题。 */
  onTurnFinished?: () => void;
}

export function useChatRuntime(options: RuntimeOptions = {}) {
  const store = useConversationStore();
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
	const [sessionCollections, setSessionCollections] = useState<string[]>([]);
	const [sessionReasoningEffort, setSessionReasoningEffort] = useState("off");
  const scrollRef = useRef<HTMLDivElement>(null);
  // 用户向上翻看历史时不要把他拽回底部。
  const stickToBottom = useRef(true);

  const refreshConversations = useCallback(async () => {
    try {
      setConversations(await chatRepository.listConversations());
    } catch {
      // 保留上一份可用列表,列表刷新失败不值得打断对话。
    }
  }, []);

  useEffect(() => {
    void refreshConversations();
  }, [refreshConversations]);

  // 订阅一次即覆盖全部会话:事件里带 threadId,store 会自行过滤。
  useEffect(() => {
    return onProtocolEvent((event) => {
      useConversationStore.getState().applyEvent(event);
      if (
        event.kind === "turn.completed" ||
        event.kind === "turn.aborted" ||
        event.kind === "stream.error"
      ) {
        setSending(false);
        options.onTurnFinished?.();
      }
    });
    // onTurnFinished 每次渲染都是新函数,放进依赖会反复重订阅;它只用于刷新
    // 列表,读最新引用即可。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 新内容到达时跟到底部,除非用户正在向上翻阅。
  useEffect(() => {
    if (!stickToBottom.current) return;
    const node = scrollRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [store.items, store.buffers]);

  const acknowledgeScroll = useCallback(() => {
    const node = scrollRef.current;
    if (!node) return;
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
    stickToBottom.current = distance < 80;
  }, []);

  const openConversation = useCallback(async (id: string) => {
    try {
      const thread = await chatRepository.getConversation(id);
      useConversationStore.getState().openThread(thread);
		setSessionCollections(thread.collections ?? []);
		setSessionReasoningEffort(thread.reasoningEffort || "off");
      setSending(false);
      stickToBottom.current = true;
    } catch (err) {
      useConversationStore.getState().setError(errorText(err));
    }
  }, []);

  const newConversation = useCallback(() => {
    useConversationStore.getState().reset();
    setInput("");
		setSessionCollections([]);
		setSessionReasoningEffort("off");
    setSending(false);
    stickToBottom.current = true;
  }, []);

  const send = useCallback(
    async (request: {
      mode: string;
      provider: string;
      model: string;
      policy: string;
		collections?: string[];
		attachments?: Attachment[];
		reasoningEffort?: string;
    }) => {
      const prompt = input.trim();
      if (!prompt) return;
      if (!request.provider || !request.model) {
        useConversationStore.getState().setError("请先在设置里启用一个模型");
        return;
      }

		setInput("");
		setSessionCollections(request.collections ?? []);
		setSessionReasoningEffort(request.reasoningEffort ?? "off");
      setSending(true);
      const current = useConversationStore.getState();
      try {
        let threadId = current.conversationId;
        if (!threadId) {
          // 会话在第一次发送时才真正创建,避免留下一串空会话。
          const created = await chatRepository.createConversation(request.mode);
          threadId = created.id;
          useConversationStore.getState().openThread(created);
        }
        await chatRepository.sendTurn({
          threadId,
          text: prompt,
          mode: request.mode,
          provider: request.provider,
          model: request.model,
			collections: request.collections,
			attachments: request.attachments,
			reasoningEffort: request.reasoningEffort,
          policy: request.policy,
        });
      } catch (err) {
        setSending(false);
        useConversationStore.getState().setError(errorText(err));
        return;
      }
      void refreshConversations();
    },
    [input, refreshConversations],
  );

  const stop = useCallback(async () => {
    const { conversationId } = useConversationStore.getState();
    if (!conversationId) return;
    try {
      await chatRepository.interrupt(conversationId);
    } catch (err) {
      useConversationStore.getState().setError(errorText(err));
    } finally {
      setSending(false);
    }
  }, []);

  const resolveApproval = useCallback(async (approve: boolean, remember: boolean) => {
    const { conversationId, approval } = useConversationStore.getState();
    if (!conversationId || !approval) return;
    useConversationStore.getState().clearApproval();
    try {
      await chatRepository.respondApproval(conversationId, approval.id, approve, remember);
    } catch (err) {
      useConversationStore.getState().setError(errorText(err));
    }
  }, []);

  const deleteConversation = useCallback(
    async (id: string) => {
      try {
        await chatRepository.deleteConversation(id);
        if (useConversationStore.getState().conversationId === id) newConversation();
        await refreshConversations();
      } catch (err) {
        useConversationStore.getState().setError(errorText(err));
      }
    },
    [newConversation, refreshConversations],
  );

  const renameConversation = useCallback(
    async (id: string, title: string) => {
      try {
        await chatRepository.renameConversation(id, title);
        await refreshConversations();
      } catch (err) {
        useConversationStore.getState().setError(errorText(err));
      }
    },
    [refreshConversations],
  );

  const hasContent = store.items.length > 0 || sending;

  const usage = useMemo(() => store.usage, [store.usage]);

  return {
    conversations,
    conversationId: store.conversationId,
    items: store.items,
    buffers: store.buffers,
    running: store.running,
    approval: store.approval,
    usage,
    error: store.error,
    interrupted: store.interrupted,
    input,
		sessionCollections,
		sessionReasoningEffort,
    sending,
    hasContent,
    scrollRef,
    setInput,
    send,
    stop,
    resolveApproval,
    newConversation,
    openConversation,
    deleteConversation,
    renameConversation,
    refreshConversations,
    onScroll: acknowledgeScroll,
  };
}
