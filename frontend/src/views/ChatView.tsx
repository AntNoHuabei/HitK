// 对话视图:把侧栏、视口、输入区接在一起。
//
// 这里是唯一把「模式 → 默认模型 → 提交 Op」串起来的地方,所以模式切换、知识库
// 范围、审批策略的变化都从这里下发。
import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Flex } from "antd";
import { knowledgeRepository, settingsRepository, appRepository, errorText } from "../shared/repositories";
import type { CollectionLite } from "../shared/types/knowledge";
import type { Attachment } from "../shared/repositories";
import { ConversationComposer } from "../features/chat/components/ConversationComposer";
import { ConversationSidebar } from "../features/chat/components/ConversationSidebar";
import { ConversationViewport } from "../features/chat/components/ConversationViewport";
import { useChatRuntime } from "../features/chat/controllers/useChatRuntime";
import { optionKey, useModelControls } from "../features/chat/controllers/useModelControls";
import { useShellStore } from "../features/shell/shellStore";

const QUICK_PROMPTS = [
  "把知识库里跟这个主题相关的资料整理成一份摘要",
  "这几份文档对同一件事的说法一致吗？",
  "帮我找出政策里提到退款时限的原文",
];

/** 上下文用量按字符估算:精确的 token 数只有产出后才能拿到。 */
function estimateTokens(items: { text?: string; reasoning?: string }[]): number {
  let chars = 0;
  for (const item of items) chars += (item.text?.length ?? 0) + (item.reasoning?.length ?? 0);
  return Math.round(chars / 1.5);
}

export default function ChatView() {
  const { message } = AntApp.useApp();
  const mode = useShellStore((state) => state.mode);
  const view = useShellStore((state) => state.view);
  const setMode = useShellStore((state) => state.setMode);
  const navigate = useShellStore((state) => state.navigate);
  const requestNewChat = useShellStore((state) => state.requestNewChat);
  const requestOpenConversation = useShellStore((state) => state.openConversation);
  const sidebarOpen = useShellStore((state) => state.sidebarOpen);
  const newChatVersion = useShellStore((state) => state.newChatVersion);
  const openRequest = useShellStore((state) => state.openConversationRequest);

  const runtime = useChatRuntime();
  const models = useModelControls(
    mode,
    view === "chat",
    `${newChatVersion}:${openRequest.version}:${runtime.conversationId}`,
  );

  const [policy, setPolicy] = useState("auto");
  const [collections, setCollections] = useState<CollectionLite[]>([]);
	const [collectionIds, setCollectionIds] = useState<string[]>([]);
	const [attachments, setAttachments] = useState<Attachment[]>([]);
	const [reasoningEffort, setReasoningEffort] = useState("off");

  const loadCollections = useCallback(async () => {
    try {
      setCollections(await knowledgeRepository.collections());
    } catch {
      // 知识库列表拉取失败不该挡住对话;范围选择退回"全部"即可。
    }
  }, []);

  useEffect(() => {
    void settingsRepository
      .get()
      .then((payload) => setPolicy(payload.policy || "auto"))
      .catch(() => undefined);
    void loadCollections();
  }, [loadCollections]);

	useEffect(() => {
		setCollectionIds(runtime.sessionCollections);
		setReasoningEffort(runtime.sessionReasoningEffort);
	}, [runtime.conversationId, runtime.sessionCollections, runtime.sessionReasoningEffort]);

	useEffect(() => {
		const choices = models.reasoningOptions;
		if (choices.length === 0) {
			if (reasoningEffort !== "off") setReasoningEffort("off");
			return;
		}
		if (!choices.includes(reasoningEffort)) {
			setReasoningEffort(choices.includes("medium") ? "medium" : choices[0]);
		}
	}, [models.active?.model, models.active?.provider, models.reasoningOptions, reasoningEffort]);

  useEffect(() => {
    if (newChatVersion > 0) runtime.newConversation();
    // newChatVersion 是让"新建对话"可以重复触发的计数器。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newChatVersion]);

  useEffect(() => {
    if (openRequest.id) void runtime.openConversation(openRequest.id);
    // 同上,version 允许重复打开同一个会话。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openRequest.version]);

  const changePolicy = useCallback(
    async (next: string) => {
      setPolicy(next);
      try {
        await settingsRepository.saveOptions(0, 0, "", "");
      } catch {
        // 保存选项失败不回滚本地状态——本轮仍按用户选择执行。
      }
      try {
        await appRepository.setPolicy(next);
      } catch (err) {
        void message.error(errorText(err));
      }
    },
    [message],
  );

  const send = useCallback(() => {
    void runtime.send({
      mode,
      provider: models.active?.provider ?? "",
      model: models.active?.model ?? "",
      policy,
		collections: collectionIds,
		attachments,
		reasoningEffort,
	});
		setAttachments([]);
	}, [attachments, collectionIds, mode, models.active, policy, reasoningEffort, runtime]);

	const pickAttachments = useCallback(async () => {
		const files = await appRepository.pickFiles("选择要附加到本轮的文件");
		const images = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"]);
		setAttachments((current) => [...current, ...files.filter((path) => !current.some((item) => item.path === path)).map((path) => {
			const name = path.split(/[\\/]/).pop() || path;
			const ext = name.slice(name.lastIndexOf(".")).toLowerCase();
			return { path, name, kind: images.has(ext) ? "image" : "file", mime: images.has(ext) ? `image/${ext === ".jpg" ? "jpeg" : ext.slice(1)}` : "" };
		})]);
	}, []);

  return (
    <div className={`hitk-chat hitk-chat-shell ${sidebarOpen ? "is-sidebar-open" : "is-sidebar-collapsed"}`}>
      <ConversationSidebar
        activeView="chat"
        mode={mode}
        sending={runtime.sending}
        conversations={runtime.conversations}
        currentConversationId={runtime.conversationId}
        onModeChange={setMode}
        onNavigate={navigate}
        onNewChat={requestNewChat}
        onOpenConversation={requestOpenConversation}
        onDeleteConversation={(id) => void runtime.deleteConversation(id)}
      />
      <Flex vertical className="hitk-chat-main-shell" style={{ minWidth: 0, minHeight: 0 }}>
        <Flex vertical className="hitk-chat-main" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <ConversationViewport
            items={runtime.items}
            buffers={runtime.buffers}
            sending={runtime.sending}
            running={runtime.running}
            usage={runtime.usage}
            error={runtime.error}
            interrupted={runtime.interrupted}
            approval={runtime.approval}
            modelLabel={models.activeLabel}
            scrollRef={runtime.scrollRef}
            onScroll={runtime.onScroll}
            quickPrompts={QUICK_PROMPTS}
            onQuickPrompt={(prompt) => {
              runtime.setInput(prompt);
              document.querySelector<HTMLTextAreaElement>("#hitk-chat-input")?.focus();
            }}
            onResolveApproval={(approve, remember) => void runtime.resolveApproval(approve, remember)}
          />
          <ConversationComposer
            input={runtime.input}
            mode={mode}
            sending={runtime.sending}
            policy={policy}
            collections={collections}
			collectionIds={collectionIds}
			attachments={attachments}
            modelOptions={models.options}
            activeModelKey={
              models.active ? optionKey(models.active.provider, models.active.model) : ""
            }
			activeModelLabel={models.activeLabel}
			reasoningEffort={reasoningEffort}
			reasoningOptions={models.reasoningOptions}
            contextWindow={models.contextWindow}
            usedTokens={estimateTokens(runtime.items)}
            modelReady={models.hasUsableModel}
			modelLoading={models.loading}
            onChangeInput={runtime.setInput}
            onChangePolicy={(next) => void changePolicy(next)}
			onChangeCollections={setCollectionIds}
			onChangeAttachments={setAttachments}
			onPickFiles={pickAttachments}
			onSwitchModel={models.switchModel}
			onConfigureModels={() => navigate("settings")}
			onChangeReasoningEffort={setReasoningEffort}
            onSend={send}
            onStop={runtime.stop}
          />
        </Flex>
      </Flex>
    </div>
  );
}
