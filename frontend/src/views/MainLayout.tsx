// 应用外壳:自绘标题栏 + 视图路由。
//
// 对话永远是底层图层,知识库/设置以 feature-layer 覆盖在它右侧(左边界让开侧栏),
// 所以切换视图不会重建对话——正在流的回答不会因为去看一眼设置而中断。
import type { MouseEvent } from "react";
import { Button, Tooltip } from "antd";
import {
  BorderOutlined,
  CloseOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MinusOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { Window as WailsWindow } from "@wailsio/runtime";
import ChatView from "./ChatView";
import SettingsView from "./SettingsView";
import { KnowledgeView } from "../features/knowledge/components/KnowledgeView";
import { useShellStore } from "../features/shell/shellStore";
import type { ViewKey } from "../features/shell/shellStore";

const VIEW_LABEL: Record<ViewKey, string> = {
  chat: "对话",
  knowledge: "知识库",
  settings: "设置",
};

export default function MainLayout() {
  const view = useShellStore((state) => state.view);
  const sidebarOpen = useShellStore((state) => state.sidebarOpen);
  const toggleSidebar = useShellStore((state) => state.toggleSidebar);
  const requestNewChat = useShellStore((state) => state.requestNewChat);

  return (
    <div className="hitk-window-shell">
      <header className="hitk-window-titlebar">
        <div className="hitk-window-title-actions">
          <Tooltip title={sidebarOpen ? "收起侧栏" : "展开侧栏"}>
            <Button
              type="text"
              aria-label={sidebarOpen ? "收起侧栏" : "展开侧栏"}
              icon={sidebarOpen ? <MenuFoldOutlined /> : <MenuUnfoldOutlined />}
              onMouseUp={releaseMouseFocus}
              onClick={toggleSidebar}
            />
          </Tooltip>
          {!sidebarOpen && (
            <Tooltip title="新建对话">
              <Button
                type="text"
                aria-label="新建对话"
                icon={<PlusOutlined />}
                onMouseUp={releaseMouseFocus}
                onClick={requestNewChat}
              />
            </Tooltip>
          )}
          <span className="hitk-window-title-name">HitK</span>
        </div>
        <div className="hitk-window-title-drag" />
        <div className="hitk-window-controls" aria-label="窗口控制">
          <Button
            type="text"
            className="is-minimise"
            aria-label="最小化"
            icon={<MinusOutlined />}
            onMouseUp={releaseMouseFocus}
            onClick={() => runWindowAction(() => WailsWindow.Minimise())}
          />
          <Button
            type="text"
            className="is-maximise"
            aria-label="最大化"
            icon={<BorderOutlined />}
            onMouseUp={releaseMouseFocus}
            onClick={() => runWindowAction(() => WailsWindow.ToggleMaximise())}
          />
          <Button
            type="text"
            className="is-close"
            aria-label="关闭"
            icon={<CloseOutlined />}
            onMouseUp={releaseMouseFocus}
            onClick={() => runWindowAction(() => WailsWindow.Close())}
          />
        </div>
      </header>

      <div className="hitk-window-body hitk-app-shell">
        <main className="hitk-shell-viewport">
          <div className="hitk-shell-primary">
            <div className="hitk-shell-chat-layer">
              <ChatView />
            </div>
            {view !== "chat" && (
              // 不套一层 feature-header:知识库与设置各自都带页内标题,再加一条
              // 外壳标题会让同一个名字出现两次。
              <section
                className={`hitk-shell-feature-layer ${sidebarOpen ? "is-sidebar-open" : ""}`}
                aria-label={VIEW_LABEL[view]}
              >
                <div className="hitk-feature-content">
                  {view === "knowledge" ? <KnowledgeView /> : <SettingsView />}
                </div>
              </section>
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

/**
 * runWindowAction 只在真正的 Wails 环境里调窗口 API。
 *
 * 浏览器里调试前端(例如只跑 vite)时这些调用会抛错,静默跳过比让按钮报错好。
 */
function runWindowAction(action: () => Promise<void>) {
  const runtime = (window as typeof window & { _wails?: { environment?: unknown } })._wails;
  if (runtime?.environment) void action();
}

/** releaseMouseFocus 让按钮在点击后失焦,避免标题栏留下焦点环。 */
function releaseMouseFocus(event: MouseEvent<HTMLButtonElement>) {
  event.currentTarget.blur();
}
