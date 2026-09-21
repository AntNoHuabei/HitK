// 应用外壳状态:当前视图、侧栏开合、agent 模式(Work / Coding)。
//
// 对话与编码共用同一块对话区域,模式只影响 agent 的提示词、可用工具与默认
// 模型,因此这里存的是"模式"而不是"视图"。
import { create } from "zustand";
import type { AgentMode } from "../../shared/types/chat";

export type ViewKey = "chat" | "knowledge" | "settings";

interface ShellState {
  view: ViewKey;
  mode: AgentMode;
  sidebarOpen: boolean;
  /** 递增计数,让"新建对话"可以重复触发。 */
  newChatVersion: number;
  openConversationRequest: { id: string; version: number };
  navigate: (view: ViewKey) => void;
  setMode: (mode: AgentMode) => void;
  toggleSidebar: () => void;
  requestNewChat: () => void;
  openConversation: (id: string) => void;
}

export const useShellStore = create<ShellState>((set) => ({
  view: "chat",
  mode: "work",
  sidebarOpen: true,
  newChatVersion: 0,
  openConversationRequest: { id: "", version: 0 },
  navigate: (view) => set({ view }),
  setMode: (mode) => set({ mode }),
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  requestNewChat: () => set((state) => ({ view: "chat", newChatVersion: state.newChatVersion + 1 })),
  openConversation: (id) =>
    set((state) => ({
      view: "chat",
      openConversationRequest: { id, version: state.openConversationRequest.version + 1 },
    })),
}));
