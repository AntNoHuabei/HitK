// 右侧面板的折叠状态与当前 tab。
//
// CompanionView 会随角色切换 remount（key={activeId}），所以这两个状态放 localStorage
// 而不是组件 state，免得每切一次人就把面板重置回默认视图。
import { useCallback, useState } from "react";

export type InspectorTab = "state" | "life" | "memories";

const COLLAPSED_KEY = "hitk.companion.inspector.collapsed";
const TAB_KEY = "hitk.companion.inspector.tab";

const TABS: InspectorTab[] = ["state", "life", "memories"];

function readCollapsed(): boolean {
  return localStorage.getItem(COLLAPSED_KEY) === "1";
}

function readTab(): InspectorTab {
  const stored = localStorage.getItem(TAB_KEY);
  return TABS.includes(stored as InspectorTab) ? (stored as InspectorTab) : "state";
}

export interface InspectorController {
  collapsed: boolean;
  tab: InspectorTab;
  setCollapsed: (collapsed: boolean) => void;
  toggle: () => void;
  /** 打开某个 tab，顺带把面板展开；从摘要 chip 点进来时就是这个语义。 */
  openTab: (tab: InspectorTab) => void;
}

export function useInspectorState(): InspectorController {
  const [collapsed, setCollapsedState] = useState(readCollapsed);
  const [tab, setTabState] = useState(readTab);

  const setCollapsed = useCallback((next: boolean) => {
    localStorage.setItem(COLLAPSED_KEY, next ? "1" : "0");
    setCollapsedState(next);
  }, []);

  const toggle = useCallback(() => {
    setCollapsed(!readCollapsed());
  }, [setCollapsed]);

  const openTab = useCallback(
    (next: InspectorTab) => {
      localStorage.setItem(TAB_KEY, next);
      setTabState(next);
      setCollapsed(false);
    },
    [setCollapsed],
  );

  return { collapsed, tab, setCollapsed, toggle, openTab };
}
