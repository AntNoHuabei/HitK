// 侧栏里的「陪伴」角色分组。
//
// 后端同时只让一个角色的世界往前跑，所以这里既是导航入口，也是唯一的切换开关。
// 这个组件只负责"把注册表画出来 + 把用户意图转给 store"：列表状态在
// characterStore，未读/忙碌在 useCompanionSummary，创建表单在弹窗里。
import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Button, Tooltip } from "antd";
import {
  CheckOutlined,
  ExclamationCircleOutlined,
  HeartOutlined,
  LoadingOutlined,
  PlusOutlined,
  RightOutlined,
} from "@ant-design/icons";
import { errorText, type CompanionCharacter } from "../../../shared/repositories";
import { useShellStore } from "../../shell/shellStore";
import { CharacterCreateModal } from "./CharacterCreateModal";
import { useCharacterStore } from "../model/characterStore";
import { useCompanionSummary } from "../model/useCompanionSummary";

const EXPAND_KEY = "hitk.companion.group.expanded";

export function CharacterSidebarGroup() {
  const { message } = AntApp.useApp();
  // 当前视图直接读 shell store：ChatView 是常驻底层，它传给侧栏的 activeView
  // 永远是 "chat"，靠那个值判断不出陪伴有没有在前台。
  const view = useShellStore((state) => state.view);
  const navigate = useShellStore((state) => state.navigate);
  const characters = useCharacterStore((state) => state.characters);
  const activeId = useCharacterStore((state) => state.activeId);
  const loading = useCharacterStore((state) => state.loading);
  const switching = useCharacterStore((state) => state.switching);
  const error = useCharacterStore((state) => state.error);
  const [expanded, setExpanded] = useState(() => localStorage.getItem(EXPAND_KEY) !== "0");
  const [createOpen, setCreateOpen] = useState(false);
  const { unread, busy, profile: baseProfile } = useCompanionSummary(activeId);

  useEffect(() => {
    void useCharacterStore.getState().refresh();
  }, []);

  // 视图驱动展开态：切走就收起，别在任务对话里白占侧栏；切回陪伴时
  // 恢复上一次手动留下的选择。
  useEffect(() => {
    if (view === "companion") {
      setExpanded(localStorage.getItem(EXPAND_KEY) !== "0");
    } else {
      setExpanded(false);
    }
  }, [view]);

  const writeExpanded = useCallback((next: boolean) => {
    localStorage.setItem(EXPAND_KEY, next ? "1" : "0");
    setExpanded(next);
  }, []);

  const handleHeaderClick = useCallback(() => {
    const next = !expanded;
    writeExpanded(next);
    // 从别处展开这个分组，顺手把人带到陪伴页。
    if (next && view !== "companion") navigate("companion");
  }, [expanded, navigate, view, writeExpanded]);

  const switchTo = useCallback(
    async (character: CompanionCharacter) => {
      navigate("companion");
      if (character.id === activeId) return;
      try {
        await useCharacterStore.getState().switchTo(character.id);
      } catch (cause) {
        message.warning(errorText(cause));
      }
    },
    [activeId, message, navigate],
  );

  const handleCreated = useCallback(
    (created: CompanionCharacter) => {
      // 后端只在当前角色空闲时才会顺手切过去，跟着它的决定走。
      if (created.active) navigate("companion");
    },
    [navigate],
  );

  const locked = busy || switching !== "";
  const activeName = characters.find((character) => character.id === activeId)?.name ?? "";

  return (
    <div className="hitk-companion-group">
      <Button
        type="text"
        className={`hitk-chat-workspace-item hitk-companion-group-header${
          view === "companion" ? " is-active" : ""
        }`}
        aria-expanded={expanded}
        aria-controls="hitk-companion-character-list"
        onClick={handleHeaderClick}
      >
        <HeartOutlined />
        <span className="hitk-companion-group-label">陪伴</span>
        <span className="hitk-companion-group-tail">
          {error ? (
            <Tooltip title={`角色列表加载失败：${error}`}>
              <ExclamationCircleOutlined className="hitk-companion-group-error-icon" />
            </Tooltip>
          ) : null}
          {unread > 0 ? <span className="hitk-chat-nav-count">{unread}</span> : null}
          <RightOutlined className="hitk-chat-workspace-chevron" />
        </span>
      </Button>

      {expanded ? (
        <div
          id="hitk-companion-character-list"
          className="hitk-companion-character-list"
          role="group"
          aria-label="陪伴角色"
          aria-busy={loading}
        >
          {loading ? (
            <>
              <span className="hitk-companion-character-skeleton" />
              <span className="hitk-companion-character-skeleton" />
              <span className="hitk-companion-character-skeleton" />
            </>
          ) : error ? null : (
            characters.map((character) => {
              const isActive = character.id === activeId;
              const isSwitching = switching === character.id;
              // 正在切过去的那一行保持正常亮度，它靠尾部转圈表达状态，不该看起来像被禁用。
              const disabled = locked && !isActive && !isSwitching;
              const tooltip = isSwitching
                ? "正在切换…"
                : disabled && busy
                  ? activeName
                    ? `${activeName}正在回复，先等这一轮结束`
                    : "正在回复，先等这一轮结束"
                  : "";
              return (
                <Tooltip key={character.id} title={tooltip}>
                  <Button
                    type="text"
                    className={`hitk-companion-character${isActive ? " is-active" : ""}`}
                    aria-current={isActive ? "page" : undefined}
                    disabled={disabled}
                    onClick={() => void switchTo(character)}
                  >
                    <span className="hitk-companion-character-avatar" aria-hidden="true">
                      {character.avatar?.trim() || character.name.slice(0, 1) || "陪"}
                    </span>
                    <span className="hitk-companion-character-name">{character.name}</span>
                    <span className="hitk-companion-character-tail">
                      {isSwitching ? (
                        <LoadingOutlined spin />
                      ) : isActive ? (
                        <CheckOutlined />
                      ) : null}
                    </span>
                  </Button>
                </Tooltip>
              );
            })
          )}

          {/* 角色上限由后端把关，这里不再抄一份常量；到了上限就让它报错。 */}
          <Button
            type="text"
            className="hitk-companion-character hitk-companion-character-add"
            onClick={() => setCreateOpen(true)}
          >
            <span className="hitk-companion-character-avatar" aria-hidden="true">
              <PlusOutlined />
            </span>
            <span className="hitk-companion-character-name">新建角色</span>
            <span className="hitk-companion-character-tail" />
          </Button>
        </div>
      ) : null}

      <CharacterCreateModal
        open={createOpen}
        baseProfile={baseProfile}
        onCancel={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />
    </div>
  );
}
