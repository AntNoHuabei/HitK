// 会话侧栏。
//
// 左上角切换 Work / Coding —— 对话与编码共用同一块对话区域,模式只改变 agent
// 的提示词、可用工具与默认模型,所以这里是唯一的模式入口。
import { useState } from "react";
import { Button, Dropdown, Flex, Popover, Tooltip, Typography } from "antd";
import {
  BgColorsOutlined,
  CheckOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  HistoryOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import type { AgentMode, ConversationSummary } from "../../../shared/types/chat";
import { HITK_THEMES, useHitKTheme } from "../../../theme/ThemeContext";
import type { ViewKey } from "../../shell/shellStore";

const { Text } = Typography;

interface Props {
  activeView: ViewKey;
  mode: AgentMode;
  sending: boolean;
  conversations: ConversationSummary[];
  currentConversationId: string;
  onModeChange: (mode: AgentMode) => void;
  onNavigate: (view: ViewKey) => void;
  onNewChat: () => void;
  onOpenConversation: (id: string) => void;
  onDeleteConversation: (id: string) => void | Promise<void>;
}

export function ConversationSidebar({
  activeView,
  mode,
  sending,
  conversations,
  currentConversationId,
  onModeChange,
  onNavigate,
  onNewChat,
  onOpenConversation,
  onDeleteConversation,
}: Props) {
  const [scrolled, setScrolled] = useState(false);

  const navItems = [
    { key: "knowledge" as const, label: "知识库", icon: <DatabaseOutlined /> },
  ];

  return (
    <aside className="hitk-chat-app-sidebar" aria-label="应用导航与会话">
      <Dropdown
        trigger={["click"]}
        placement="bottomLeft"
        disabled={sending}
        menu={{
          selectedKeys: [mode],
          onClick: ({ key }) => onModeChange(key as AgentMode),
          items: [
            {
              key: "work",
              label: (
                <span className="hitk-chat-profile-option">
                  <span>
                    <strong>Work</strong>
                    <small>知识库检索、写作与日常事务</small>
                  </span>
                  {mode === "work" && <CheckOutlined />}
                </span>
              ),
            },
            {
              key: "coding",
              label: (
                <span className="hitk-chat-profile-option">
                  <span>
                    <strong>Coding</strong>
                    <small>读写代码、搜索与执行命令</small>
                  </span>
                  {mode === "coding" && <CheckOutlined />}
                </span>
              ),
            },
          ],
        }}
      >
        <Tooltip title={sending ? "生成中无法切换模式" : "切换 Work / Coding，默认模型也随之切换"}>
          <Button type="text" className="hitk-chat-profile-trigger" disabled={sending}>
            <strong>{mode === "coding" ? "Coding" : "Work"}</strong>
            <DownOutlined />
          </Button>
        </Tooltip>
      </Dropdown>

      <Button
        type="text"
        className="hitk-chat-new-session"
        icon={<EditOutlined />}
        onClick={() => {
          onNewChat();
          onNavigate("chat");
        }}
      >
        <span>新建对话</span>
      </Button>

      <div
        className={`hitk-chat-sidebar-scroll ${scrolled ? "is-scrolled" : ""}`}
        onScroll={(event) => setScrolled(event.currentTarget.scrollTop > 0)}
      >
        <nav className="hitk-chat-app-nav" aria-label="功能菜单">
          {navItems.map((item) => (
            <Button
              key={item.key}
              type="text"
              className={item.key === activeView ? "is-active" : undefined}
              icon={item.icon}
              onClick={() => onNavigate(item.key)}
            >
              <span>{item.label}</span>
            </Button>
          ))}
        </nav>

        <div className="hitk-chat-workspace-list">
          <div className="hitk-chat-sidebar-section-label">历史对话</div>
          {conversations.length === 0 ? (
            <Text type="secondary" className="hitk-chat-empty-history">
              还没有对话
            </Text>
          ) : (
            <div className="hitk-chat-history-list">
              {conversations.map((conversation) => {
                const active = conversation.id === currentConversationId;
                return (
                  <div
                    key={conversation.id}
                    className={`hitk-chat-history-item ${active ? "is-active" : ""}`}
                  >
                    <Button
                      type="text"
                      className="hitk-chat-history-open"
                      aria-current={active ? "page" : undefined}
                      onClick={() => onOpenConversation(conversation.id)}
                    >
                      <span className="hitk-chat-history-item-label">
                        <HistoryOutlined />
                        <span>{conversation.title}</span>
                      </span>
                    </Button>
                    {active && (
                      <Tooltip title="删除当前对话">
                        <Button
                          type="text"
                          className="hitk-chat-history-delete"
                          aria-label="删除当前对话"
                          icon={<DeleteOutlined />}
                          onClick={() => void onDeleteConversation(conversation.id)}
                        />
                      </Tooltip>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <div className="hitk-chat-sidebar-footer">
        <ThemeSwitcher />
        <Button
          type="text"
          className={activeView === "settings" ? "is-active" : undefined}
          icon={<SettingOutlined />}
          onClick={() => onNavigate("settings")}
        >
          <span>设置</span>
        </Button>
      </div>
    </aside>
  );
}

function ThemeSwitcher() {
  const { themeId, setTheme } = useHitKTheme();
  const panel = (
    <Flex vertical gap={4} className="hitk-theme-panel">
      <Typography.Text type="secondary" className="hitk-theme-panel-title">
        选择主题
      </Typography.Text>
      {HITK_THEMES.map((theme) => (
        <Button
          key={theme.id}
          size="small"
          type="text"
          className={`hitk-theme-option ${theme.id === themeId ? "is-active" : ""}`}
          aria-pressed={theme.id === themeId}
          onClick={() => void setTheme(theme.id)}
        >
          <span
            className="hitk-theme-swatch"
            style={{ backgroundColor: theme.vars["sidebar-bg"], borderColor: theme.vars.divider }}
          >
            <span style={{ backgroundColor: theme.vars.signal }} />
          </span>
          <span className="hitk-theme-option-label">{theme.name}</span>
          {theme.id === themeId && <CheckOutlined className="hitk-theme-option-check" />}
        </Button>
      ))}
    </Flex>
  );
  return (
    <Popover content={panel} placement="rightBottom" trigger="click">
      <Button type="text" icon={<BgColorsOutlined />}>
        <span>主题</span>
      </Button>
    </Popover>
  );
}
