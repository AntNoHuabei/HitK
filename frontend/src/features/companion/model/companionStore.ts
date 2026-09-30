import { create } from "zustand";
import { EventKind, ItemStatus, ItemType } from "../../../shared/repositories";
import type { Item, ProtocolEvent, Thread, Usage } from "../../../shared/repositories";

interface Buffers {
  action: Record<string, string>;
  text: Record<string, string>;
  reasoning: Record<string, string>;
}

interface CompanionConversationState {
  /** 当前这份会话属于哪个角色,用于丢弃别的角色的迟到响应。 */
  characterId: string;
  threadId: string;
  items: Item[];
  buffers: Buffers;
  running: boolean;
  usage: Usage | null;
  error: string;
  interrupted: boolean;
  lastSeq: number;
  /** 声明当前角色。换人时清空,同角色重复调用保持不变。 */
  claim: (characterId: string) => void;
  openThread: (characterId: string, thread: Thread) => void;
  setRunning: (running: boolean) => void;
  setError: (error: string) => void;
  applyEvent: (event: ProtocolEvent) => void;
  reset: () => void;
}

const emptyBuffers = (): Buffers => ({ action: {}, text: {}, reasoning: {} });

function upsertItem(items: Item[], item: Item): Item[] {
  const index = items.findIndex((existing) => existing.id === item.id);
  if (index === -1) return [...items, item];
  const next = items.slice();
  next[index] = { ...next[index], ...item };
  return next;
}

function dropNullItems(items: (Item | null)[] | null | undefined): Item[] {
  return (items ?? []).filter((item): item is Item => item != null);
}

export const useCompanionStore = create<CompanionConversationState>((set) => ({
  characterId: "",
  threadId: "",
  items: [],
  buffers: emptyBuffers(),
  running: false,
  usage: null,
  error: "",
  interrupted: false,
  lastSeq: 0,

  reset: () =>
    set({
      characterId: "",
      threadId: "",
      items: [],
      buffers: emptyBuffers(),
      running: false,
      usage: null,
      error: "",
      interrupted: false,
      lastSeq: 0,
    }),

  claim: (characterId) =>
    set((state) =>
      state.characterId === characterId
        ? state
        : {
            characterId,
            threadId: "",
            items: [],
            buffers: emptyBuffers(),
            running: false,
            usage: null,
            error: "",
            interrupted: false,
            lastSeq: 0,
          },
    ),

  openThread: (characterId, thread) =>
    set((state) => {
      // 快速切换角色时,旧角色那条较慢的 getThread 可能后到。身份不符就整批丢弃。
      if (state.characterId && characterId !== state.characterId) return state;
      return {
        characterId,
        threadId: thread.id,
        items: dropNullItems(thread.items),
        buffers: emptyBuffers(),
        running: false,
        usage: null,
        error: "",
        interrupted: false,
        lastSeq: 0,
      };
    }),

  setRunning: (running) => set({ running }),
  setError: (error) => set({ error }),

  applyEvent: (event) =>
    set((state) => {
      if (event.threadId && event.threadId !== state.threadId) {
        return state;
      }
      if (event.seq > 0 && event.seq <= state.lastSeq) return state;
      const next = { ...state, lastSeq: Math.max(state.lastSeq, event.seq ?? 0) };
      switch (event.kind) {
        case EventKind.EvTurnStarted:
          return { ...next, running: true, error: "", interrupted: false };
        case EventKind.EvItemStarted: {
          if (!event.item) return next;
          return {
            ...next,
            items: upsertItem(next.items, {
              ...event.item,
              text: event.item.text ?? "",
              reasoning: event.item.reasoning ?? "",
              status: ItemStatus.StatusInProgress,
            }),
          };
        }
        case EventKind.EvItemDelta: {
          const delta = event.delta;
          if (!delta) return next;
          const channel = delta.channel === "reasoning" ? "reasoning" : "text";
          const buffers: Buffers = {
            action: { ...next.buffers.action },
            text: { ...next.buffers.text },
            reasoning: { ...next.buffers.reasoning },
          };
          if (delta.channel === "action" || delta.channel === "speech") {
            const target = delta.channel === "action" ? buffers.action : buffers.text;
            target[delta.itemId] = (target[delta.itemId] ?? "") + delta.text;
            return { ...next, buffers };
          }
          buffers[channel][delta.itemId] = (buffers[channel][delta.itemId] ?? "") + delta.text;
          return { ...next, buffers };
        }
        case EventKind.EvItemCompleted: {
          if (!event.item) return next;
          const buffers: Buffers = {
            action: { ...next.buffers.action },
            text: { ...next.buffers.text },
            reasoning: { ...next.buffers.reasoning },
          };
          delete buffers.action[event.item.id];
          delete buffers.text[event.item.id];
          delete buffers.reasoning[event.item.id];
          return {
            ...next,
            buffers,
            items: upsertItem(next.items, {
              ...event.item,
              status: event.item.status || ItemStatus.StatusCompleted,
            }),
          };
        }
        case EventKind.EvTurnCompleted:
          return { ...next, running: false, usage: event.usage ?? next.usage };
        case EventKind.EvTurnAborted:
          return {
            ...next,
            running: false,
            interrupted: true,
            usage: event.usage ?? next.usage,
          };
        case EventKind.EvTokenCount:
          return { ...next, usage: event.usage ?? next.usage };
        case EventKind.EvStreamError:
          return { ...next, running: false, error: event.error ?? "生成失败" };
        default:
          return next;
      }
    }),
}));

export function pendingCompanionText(
  state: CompanionConversationState,
  item: Item,
): string {
  const channel = item.type === ItemType.ItemReasoning ? "reasoning" : "text";
  return state.buffers[channel][item.id] ?? (channel === "reasoning" ? item.reasoning : item.text) ?? "";
}
