// 对话状态归约。
//
// 事件 → 状态的转换只在这里发生:组件订阅 store,不直接订阅事件流。这样
// "文本正在流式输出""工具跑了多久""这轮被中断"共用同一条归约路径,UI 上
// 没有特例分支。
import { create } from "zustand";
import { EventKind, ItemStatus, ItemType } from "../../../shared/repositories";
import type {
  ApprovalRequest,
  Item,
  ProtocolEvent,
  Thread,
  Usage,
} from "../../../shared/repositories";

/** 流式缓冲:同一 item 的文本与推理分别累积。 */
interface Buffers {
  text: Record<string, string>;
  reasoning: Record<string, string>;
}

interface ConversationState {
  conversationId: string;
  title: string;
  mode: string;
  items: Item[];
  buffers: Buffers;
  running: boolean;
  turnId: string;
  approval: ApprovalRequest | null;
  usage: Usage | null;
  error: string;
  interrupted: boolean;
  lastSeq: number;

  reset: () => void;
  openThread: (thread: Thread) => void;
  setRunning: (running: boolean) => void;
  setError: (error: string) => void;
  clearApproval: () => void;
  applyEvent: (event: ProtocolEvent) => void;
}

const emptyBuffers = (): Buffers => ({ text: {}, reasoning: {} });

/** upsertItem 保持既有顺序:更新就地替换,新增追加到末尾。 */
function upsertItem(items: Item[], item: Item): Item[] {
  const index = items.findIndex((existing) => existing.id === item.id);
  if (index === -1) return [...items, item];
  const next = items.slice();
  next[index] = { ...next[index], ...item };
  return next;
}

/**
 * dropNullItems 过滤切片中的空位。
 *
 * Go 的 nil slice 过桥是 null,元素级指针也可能落成 null,所以列表必须先过
 * 一遍再进状态。
 */
function dropNullItems(items: (Item | null)[] | null | undefined): Item[] {
  return (items ?? []).filter((item): item is Item => item != null);
}

export const useConversationStore = create<ConversationState>((set) => ({
  conversationId: "",
  title: "",
  mode: "work",
  items: [],
  buffers: emptyBuffers(),
  running: false,
  turnId: "",
  approval: null,
  usage: null,
  error: "",
  interrupted: false,
  lastSeq: 0,

  reset: () =>
    set({
      conversationId: "",
      title: "",
      items: [],
      buffers: emptyBuffers(),
      running: false,
      turnId: "",
      approval: null,
      usage: null,
      error: "",
      interrupted: false,
      lastSeq: 0,
    }),

  openThread: (thread) =>
    set({
      conversationId: thread.id,
      title: thread.title,
      mode: thread.mode ?? "work",
      items: dropNullItems(thread.items),
      buffers: emptyBuffers(),
      running: false,
      turnId: "",
      approval: null,
      usage: null,
      error: "",
      interrupted: false,
      lastSeq: 0,
    }),

  setRunning: (running) => set({ running }),

  setError: (error) => set({ error }),

  clearApproval: () => set({ approval: null }),

  applyEvent: (event) =>
    set((state) => {
      // 同一线程之外的事件与本会话无关;seq 保证重复投递不会重复渲染。
      if (event.threadId && event.threadId !== state.conversationId) {
        return state;
      }
      if (event.seq > 0 && event.seq <= state.lastSeq) return state;
      const next = { ...state, lastSeq: Math.max(state.lastSeq, event.seq ?? 0) };

      switch (event.kind) {
        case EventKind.EvTurnStarted:
          return { ...next, running: true, turnId: event.turnId ?? "", error: "", interrupted: false };

        case EventKind.EvItemStarted: {
          if (!event.item) return next;
          const item = event.item;
          // 文本类 item 先占位,后续 delta 才有地方累积。
          const seeded: Item = {
            ...item,
            text: item.text ?? "",
            reasoning: item.reasoning ?? "",
            status: ItemStatus.StatusInProgress,
          };
          return { ...next, items: upsertItem(next.items, seeded) };
        }

        case EventKind.EvItemDelta: {
          const delta = event.delta;
          if (!delta) return next;
          const channel = delta.channel === "reasoning" ? "reasoning" : "text";
          const buffers: Buffers = {
            text: { ...next.buffers.text },
            reasoning: { ...next.buffers.reasoning },
          };
          buffers[channel][delta.itemId] = (buffers[channel][delta.itemId] ?? "") + delta.text;
          return { ...next, buffers };
        }

        case EventKind.EvItemCompleted: {
          if (!event.item) return next;
          // 完成态携带权威文本,缓冲随即丢弃,避免把同一段话渲染两遍。
          const buffers: Buffers = {
            text: { ...next.buffers.text },
            reasoning: { ...next.buffers.reasoning },
          };
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
        case EventKind.EvApprovalNeeded:
          return { ...next, approval: event.approval ?? null };
        case EventKind.EvTokenCount:
          return { ...next, usage: event.usage ?? next.usage };
        case EventKind.EvStreamError:
          return { ...next, running: false, error: event.error ?? "生成失败" };
        case EventKind.EvNotice:
          return { ...next, error: event.notice ?? "" };
        default:
          return next;
      }
    }),
}));

/** pendingText 返回某个文本 item 当前应渲染的内容。 */
export function pendingText(state: ConversationState, item: Item): string {
  const channel = item.type === ItemType.ItemReasoning ? "reasoning" : "text";
  const buffered = state.buffers[channel][item.id];
  if (buffered != null) return buffered;
  return (channel === "reasoning" ? item.reasoning : item.text) ?? "";
}
