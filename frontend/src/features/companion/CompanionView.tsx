import { useCallback, useEffect, useMemo, useState } from "react";
import {
  App as AntApp,
  Button,
  Drawer,
  Flex,
  Input,
  Modal,
  Segmented,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  CloudOutlined,
  DoubleLeftOutlined,
  DoubleRightOutlined,
  ExclamationCircleFilled,
  HeartOutlined,
  ReloadOutlined,
  SendOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import {
  companionRepository,
  errorText,
  type CompanionMemory,
  type CompanionOverview,
  type CompanionProfile,
  type LifeEvent,
} from "../../shared/repositories";
import { optionKey } from "../../shared/lib/modelKey";
import { onCompanionEvent, onProtocolEvent } from "../../shared/wails/events";
import { ConversationMessages } from "../chat/components/ConversationMessages";
import { ModelControl } from "../chat/components/ModelControl";
import { useShellStore } from "../shell/shellStore";
import { LifePanel } from "./components/panels/LifePanel";
import { MemoryEditor } from "./components/panels/MemoryEditor";
import { MemoryPanel } from "./components/panels/MemoryPanel";
import { SettingsPanel } from "./components/panels/SettingsPanel";
import { StatePanel } from "./components/panels/StatePanel";
import { StatusMeter } from "./components/panels/StatusMeter";
import { TurnTaskBanner } from "./components/panels/TurnTaskBanner";
import { useCharacterStore } from "./model/characterStore";
import { useCharacterModel } from "./model/useCharacterModel";
import { useInspectorState, type InspectorTab } from "./model/inspectorState";
import { notableMetrics } from "./model/metricSalience";
import { useCompanionStore } from "./model/companionStore";

const { Text, Title } = Typography;
const NOTICE_KEY = "hitk.companion.notice.v1";

export function CompanionView() {
  const { message } = AntApp.useApp();
  const navigate = useShellStore((state) => state.navigate);
  const store = useCompanionStore();
  const activeId = useCharacterStore((state) => state.activeId);
  const characters = useCharacterStore((state) => state.characters);
  const [overview, setOverview] = useState<CompanionOverview | null>(null);
  const [profile, setProfile] = useState<CompanionProfile | null>(null);
  const [events, setEvents] = useState<LifeEvent[]>([]);
  const [memories, setMemories] = useState<CompanionMemory[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [loading, setLoading] = useState(true);
  const inspector = useInspectorState();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [noticeOpen, setNoticeOpen] = useState(() => localStorage.getItem(NOTICE_KEY) !== "1");
  const [memoryEditor, setMemoryEditor] = useState<CompanionMemory | null>(null);
  const [memoryModalOpen, setMemoryModalOpen] = useState(false);

  const refresh = useCallback(async () => {
    // 抓住发起时的角色，回来时若已切走就整批丢弃，避免旧角色数据盖到新角色上。
    const characterId = useCharacterStore.getState().activeId;
    const [nextOverview, nextEvents, nextMemories, thread] = await Promise.all([
      companionRepository.overview(),
      companionRepository.listEvents(),
      companionRepository.listMemories(),
      companionRepository.getThread(),
    ]);
    if (useCharacterStore.getState().activeId !== characterId) return null;
    setOverview(nextOverview);
    setProfile(nextOverview.profile);
    setEvents(nextEvents);
    setMemories(nextMemories);
    useCompanionStore.getState().openThread(characterId, thread);
    useCompanionStore.getState().setRunning(await companionRepository.running());
    return nextOverview;
  }, []);

  const saveProfile = useCallback(async (next: CompanionProfile) => {
    const saved = await companionRepository.saveProfile(next);
    setProfile(saved);
    setOverview((current) => (current ? { ...current, profile: saved } : current));
    return saved;
  }, []);

  const models = useCharacterModel(profile, true, saveProfile);

  // 声明这份会话 store 归属当前角色，换人时清空，迟到的旧响应会被丢弃。
  useEffect(() => {
    if (activeId) useCompanionStore.getState().claim(activeId);
  }, [activeId]);

  useEffect(() => {
    void useCharacterStore.getState().refresh();
  }, []);

  useEffect(() => {
    setLoading(true);
    void refresh()
      .then(() => companionRepository.markRead())
      .catch((err) => message.error(errorText(err)))
      .finally(() => setLoading(false));
  }, [message, refresh]);

  useEffect(() => {
    const offProtocol = onProtocolEvent((event) => {
      useCompanionStore.getState().applyEvent(event);
      if (event.kind === "turn.completed" || event.kind === "turn.aborted" || event.kind === "stream.error") {
        setSending(false);
        void refresh().catch(() => undefined);
      }
    });
    const offCompanion = onCompanionEvent((event) => {
      // 别的角色的事件不该改动当前视图。切换后旧角色仍可能有余波事件到达。
      if (event.characterId && event.characterId !== useCharacterStore.getState().activeId) {
        return;
      }
      setOverview(event.overview);
      setProfile((current) => {
        if (!current || event.kind !== "profile.updated") return event.overview.profile;
        return current;
      });
      if (event.kind === "message.created" || event.kind === "reset" || event.kind === "state.updated") {
        void companionRepository.listEvents().then(setEvents).catch(() => undefined);
      }
      if (event.kind === "task.updated" && event.overview.activeTask) {
        const status = event.overview.activeTask.status;
        setSending(status === "aligning" || status === "resolved" || status === "generating");
      }
      if (event.kind === "task.updated" && !event.overview.activeTask) {
        setSending(false);
      }
      if (event.kind === "reset") {
        void refresh().catch(() => undefined);
      }
    });
    return () => {
      offProtocol();
      offCompanion();
    };
  }, [refresh]);

  useEffect(() => {
    if (!overview?.profile) return;
    setProfile((current) => {
      if (!current) return overview.profile;
      return { ...overview.profile, ...current, updatedAt: overview.profile.updatedAt };
    });
  }, [overview?.profile]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || !models.active) return;
    setSending(true);
    useCompanionStore.getState().setError("");
    useCompanionStore.getState().setRunning(true);
    try {
      const task = await companionRepository.submitTurn(
        text,
        models.active.provider,
        models.active.model,
        profile?.reasoningEffort ?? "off",
      );
      setInput("");
      setSending(task.status === "aligning" || task.status === "resolved" || task.status === "generating");
      setOverview((current) => current ? { ...current, activeTask: task } : current);
    } catch (err) {
      setSending(false);
      useCompanionStore.getState().setRunning(false);
      useCompanionStore.getState().setError(errorText(err));
    }
  }, [input, models.active, profile?.reasoningEffort]);

  const stop = useCallback(async () => {
    try {
      await companionRepository.interrupt();
    } catch (err) {
      useCompanionStore.getState().setError(errorText(err));
    } finally {
      setSending(false);
      useCompanionStore.getState().setRunning(false);
    }
  }, []);

  const cancelTask = useCallback(async (taskID: string) => {
    try {
      await companionRepository.cancelTask(taskID);
      await refresh();
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message, refresh]);

  const setPaused = useCallback(async (paused: boolean) => {
    try {
      await companionRepository.setPaused(paused);
      setOverview((current) => current ? { ...current, paused } : current);
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message]);

  const reset = useCallback(async () => {
    try {
      await companionRepository.reset();
      await refresh();
      message.success(`${profile?.name ?? "她的"}的世界已重置`);
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message, profile, refresh]);

  const saveMemory = useCallback(async (memory: CompanionMemory) => {
    try {
      await companionRepository.saveMemory(memory);
      setMemories(await companionRepository.listMemories());
      setMemoryModalOpen(false);
      setMemoryEditor(null);
      message.success("记忆已保存");
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message]);

  const changeReasoningEffort = useCallback(async (effort: string) => {
    if (!profile) return;
    try {
      await saveProfile({ ...profile, reasoningEffort: effort });
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message, profile, saveProfile]);

  const deleteCharacter = useCallback(async () => {
    try {
      await useCharacterStore.getState().remove(activeId);
      message.success("角色已删除");
      await refresh();
    } catch (err) {
      message.error(errorText(err));
    }
  }, [activeId, message, refresh]);

  const deleteMemory = useCallback(async (id: string) => {
    try {
      await companionRepository.deleteMemory(id);
      setMemories((current) => current.filter((memory) => memory.id !== id));
    } catch (err) {
      message.error(errorText(err));
    }
  }, [message]);

  const activeModelKey = models.active
    ? optionKey(models.active.provider, models.active.model)
    : "";
  const modelOptions = useMemo(
    () => models.options.map((option) => ({
      value: optionKey(option.provider, option.model),
      label: `${option.providerName} / ${option.label || option.model}`,
    })),
    [models.options],
  );

  const onlyOneCharacter = characters.length <= 1;
  const deleteDisabled = onlyOneCharacter || sending;
  const deleteTooltip = onlyOneCharacter
    ? "至少保留一个角色"
    : sending
      ? `${profile?.name ?? "她"}正在回复`
      : "";

  // 加载态里 profile 还没到，从注册表里取名字，取不到就用中性文案。
  const loadingLabel = (() => {
    const name = characters.find((character) => character.id === activeId)?.name;
    return name ? `正在回到${name}的世界…` : "正在进入陪伴世界…";
  })();

  if (loading && !overview) {
    return <div className="hitk-companion-loading"><ReloadOutlined spin /> {loadingLabel}</div>;
  }
  if (!overview || !profile) {
    return <div className="hitk-companion-loading">陪伴模块暂时不可用。</div>;
  }

  const metricValue = (id: string) =>
    (overview.metrics ?? []).find((metric) => metric.id === id)?.value ?? 0;
  const notable = notableMetrics(overview.metrics ?? []);

  return (
    <div className="hitk-companion hitk-chat">
      <header className="hitk-companion-head">
        <div className="hitk-companion-identity">
          <span className="hitk-companion-mark" aria-hidden="true"><HeartOutlined /></span>
          <div className="hitk-companion-title">
            <Title level={3}>{profile.name}</Title>
            <Text type="secondary">{overview.state.currentActivity}</Text>
          </div>
        </div>
        <div className="hitk-companion-status">
          {/* 面板展开时详情就在右手边，头部不再重复三根条；收起时才把它们挪回来兜底。 */}
          {inspector.collapsed ? (
            <>
              <StatusMeter label="精力" value={metricValue("vitality")} />
              <StatusMeter label="压力" value={metricValue("stress")} tone="warn" />
              <StatusMeter label="想念" value={metricValue("missing")} tone="signal" />
            </>
          ) : null}
          <Tag bordered={false}>{overview.derived.mood}</Tag>
          {overview.weather ? (
            <Tooltip title={`${overview.weather.city} · 城市级天气`}>
              <Tag icon={<CloudOutlined />} bordered={false}>
                {overview.weather.description} {overview.weather.temperature}°
              </Tag>
            </Tooltip>
          ) : (
            <Tag icon={<CloudOutlined />} bordered={false}>天气未知</Tag>
          )}
          {notable.length > 0 && (
            <Tooltip title={notable.map((item) => `${item.metric.label}${item.read}`).join(" · ")}>
              <button
                type="button"
                className="hitk-companion-alert"
                onClick={() => inspector.openTab("state")}
              >
                <ExclamationCircleFilled />
                <span>{notable.length} 项需要留意</span>
              </button>
            </Tooltip>
          )}
          {overview.paused && <Tag color="default">已暂停</Tag>}
          <Tooltip title={inspector.collapsed ? `展开${profile.name}的面板` : "收起面板"}>
            <Button
              type="text"
              className="hitk-companion-toggle"
              aria-label={inspector.collapsed ? `展开${profile.name}的面板` : "收起她的面板"}
              aria-expanded={!inspector.collapsed}
              aria-controls="hitk-companion-side"
              icon={inspector.collapsed ? <DoubleRightOutlined /> : <DoubleLeftOutlined />}
              onClick={inspector.toggle}
            />
          </Tooltip>
        </div>
      </header>

      <div className={`hitk-companion-body${inspector.collapsed ? " is-collapsed" : ""}`}>
        <section className="hitk-companion-conversation" aria-label={`与${profile.name}的对话`}>
          <div className="hitk-companion-messages">
            {overview.activeTask && <TurnTaskBanner task={overview.activeTask} onCancel={cancelTask} />}
            {store.items.length === 0 ? (
              <div className="hitk-companion-empty">
                <span className="hitk-companion-empty-mark"><HeartOutlined /></span>
                <Title level={4}>{profile.name}在这里，继续自己的生活</Title>
                <Text type="secondary">发一句话，或者先看看{profile.name}最近经历了什么。</Text>
              </div>
            ) : (
              <ConversationMessages
                items={store.items}
                buffers={store.buffers}
                running={store.running || sending}
                modelLabel={models.activeLabel}
                usage={store.usage}
              />
            )}
            {store.interrupted && <div className="hitk-companion-inline-note">本轮已经停下。</div>}
            {store.error && <div className="hitk-companion-error">{store.error}</div>}
          </div>

          <div className="hitk-chat-composer hitk-companion-composer">
            <Flex justify="center" className="hitk-chat-composer-track">
              <div className="hitk-chat-composer-shell">
                <Input.TextArea
                  id="hitk-companion-input"
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                  placeholder={`和${profile.name}说点什么…`}
                  autoSize={{ minRows: 2, maxRows: 7 }}
                  variant="borderless"
                  disabled={sending && overview.activeTask?.status !== "waiting"}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !event.shiftKey) {
                      event.preventDefault();
                      if (!sending) void send();
                    }
                  }}
                />
                <div className="hitk-chat-composer-actions">
                  <div className="hitk-chat-composer-actions-left" />
                  <div className="hitk-chat-composer-actions-right">
                    <ModelControl
                      modelOptions={models.options}
                      activeModelKey={activeModelKey}
                      activeModelLabel={models.activeLabel}
                      reasoningEffort={profile.reasoningEffort ?? "off"}
                      reasoningOptions={models.reasoningOptions}
                      contextWindow={models.contextWindow}
                      usedTokens={estimateCompanionTokens(store.items)}
                      modelReady={models.hasUsableModel}
                      modelLoading={models.loading}
                      disabled={sending}
                      onChangeModel={(provider, model) => void models.switchModel(provider, model)}
                      onConfigureModels={() => navigate("settings")}
                      onChangeReasoningEffort={(effort) => void changeReasoningEffort(effort)}
                    />
                    <Tooltip title={sending ? "停止生成" : "发送"}>
                      <Button
                        type="primary"
                        className={`hitk-chat-send-btn${sending ? " hitk-stop-btn" : ""}`}
                        shape="circle"
                        aria-label={sending ? "停止生成" : "发送"}
                        icon={sending ? <span className="hitk-stop-btn-glyph" /> : <SendOutlined />}
                        disabled={!sending && (!input.trim() || !models.hasUsableModel)}
                        onClick={() => void (sending ? stop() : send())}
                      />
                    </Tooltip>
                  </div>
                </div>
              </div>
            </Flex>
          </div>
        </section>

        <aside
          id="hitk-companion-side"
          className="hitk-companion-side"
          aria-label={`${profile.name}的生活与记忆`}
        >
          <Segmented
            block
            value={inspector.tab}
            onChange={(value) => inspector.openTab(value as InspectorTab)}
            options={[
              { label: "状态", value: "state" },
              { label: "近日生活", value: "life" },
              { label: "记忆", value: "memories" },
            ]}
          />
          <div className="hitk-companion-panel">
            {inspector.tab === "state" && (
              <StatePanel
                metrics={overview.metrics ?? []}
                derived={overview.derived}
                activity={overview.state.currentActivity}
                activityEndsAt={overview.state.activityEndsAt}
                clock={overview.clock}
              />
            )}
            {inspector.tab === "life" && <LifePanel events={events} />}
            {inspector.tab === "memories" && (
              <MemoryPanel
                memories={memories}
                characterName={profile.name}
                onAdd={() => {
                  setMemoryEditor({
                    id: "",
                    kind: "user_fact",
                    content: "",
                    importance: 6,
                    strength: 6,
                    accessCount: 0,
                    createdAt: 0,
                    updatedAt: 0,
                  });
                  setMemoryModalOpen(true);
                }}
                onEdit={(memory) => {
                  setMemoryEditor(memory);
                  setMemoryModalOpen(true);
                }}
                onDelete={(id) => void deleteMemory(id)}
              />
            )}
          </div>
          <div className="hitk-companion-side-foot">
            <Button
              type="text"
              className="hitk-companion-settings-btn"
              icon={<SettingOutlined />}
              onClick={() => setSettingsOpen(true)}
            >
              {`${profile.name}的设置`}
            </Button>
          </div>
        </aside>
      </div>

      {/* 人设、模型、危险区需要真正的编辑宽度，不塞在 370px 的检视栏里。 */}
      <Drawer
        className="hitk-companion-settings-drawer"
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        width={560}
        title={`${profile.name}的设置`}
        destroyOnClose
      >
        <SettingsPanel
          profile={profile}
          paused={overview.paused}
          modelOptions={modelOptions}
          activeModelKey={activeModelKey}
          reasoningOptions={models.reasoningOptions}
          pinStale={models.pinStale}
          modelReady={models.hasUsableModel}
          deleteDisabled={deleteDisabled}
          deleteTooltip={deleteTooltip}
          onPause={(paused) => void setPaused(paused)}
          onTick={() => void companionRepository.tickNow().then(refresh).catch((err) => message.error(errorText(err)))}
          onReset={() => void reset()}
          onDelete={() => {
            setSettingsOpen(false);
            void deleteCharacter();
          }}
          onSaveProfile={saveProfile}
        />
      </Drawer>

      <MemoryEditor
        open={memoryModalOpen}
        memory={memoryEditor}
        characterName={profile.name}
        onCancel={() => {
          setMemoryModalOpen(false);
          setMemoryEditor(null);
        }}
        onSave={(memory) => void saveMemory(memory)}
      />
      <Modal
        open={noticeOpen}
        title={`关于${profile.name}`}
        okText="我知道了"
        cancelButtonProps={{ style: { display: "none" } }}
        onOk={() => {
          localStorage.setItem(NOTICE_KEY, "1");
          setNoticeOpen(false);
        }}
      >
        <Text>
          {profile.name}不是人类，所有对话、状态和记忆都由本机模型生成并保存在本地。
        </Text>
      </Modal>
    </div>
  );
}

function estimateCompanionTokens(items: { text?: string; reasoning?: string }[]): number {
  let chars = 0;
  for (const item of items) {
    chars += (item.text?.length ?? 0) + (item.reasoning?.length ?? 0);
  }
  return Math.round(chars / 1.5);
}
