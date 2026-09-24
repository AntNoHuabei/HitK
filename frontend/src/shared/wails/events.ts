// Wails 事件订阅。
//
// agent 把每个状态变化都作为一条 `hitk:event` 发布,组件从不轮询。把订阅收在
// 一处意味着去重与顺序规则只存在一个地方。
import { Events } from "@wailsio/runtime";
import type { ProtocolEvent, RuntimeProgress } from "../repositories/bindings";

export const PROTOCOL_EVENT = "hitk:event";
export const RUNTIME_PROGRESS_EVENT = "hitk:runtime-progress";

/**
 * onProtocolEvent 订阅 agent 事件。
 *
 * 事件来自单一生产者因此天然有序,但一次大段粘贴仍可能让文本增量乱序到达;
 * 消费者按 seq 保持单调即可。
 */
export function onProtocolEvent(handler: (event: ProtocolEvent) => void): () => void {
  const off = Events.On(PROTOCOL_EVENT, (ev: { data: ProtocolEvent }) => {
    if (ev?.data) handler(ev.data);
  });
  return () => {
    if (typeof off === "function") off();
  };
}

/** onRuntimeProgress subscribes to component download and installation states. */
export function onRuntimeProgress(handler: (progress: RuntimeProgress) => void): () => void {
  const off = Events.On(RUNTIME_PROGRESS_EVENT, (ev: { data: RuntimeProgress }) => {
    if (ev?.data) handler(ev.data);
  });
  return () => {
    if (typeof off === "function") off();
  };
}
