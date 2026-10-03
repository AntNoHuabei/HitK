// 转录窗口：只渲染尾部一截，滚到顶再往前补一段。
//
// 陪伴线程只会长不会短。全量渲染不只是慢，读起来也不对 —— 打开对话应该落在
// 最新那一句上，而不是守着一屏早看完的旧消息。这个 hook 管三件事：打开即贴底、
// 新内容到达时继续贴底（除非用户正在往上翻）、滚到顶再放出一页更早的。
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
  type UIEventHandler,
} from "react";

/** 一次放出的条数。比一屏多不少，免得滚两下就得等下一批。 */
const PAGE_SIZE = 60;
/** 距顶多少像素算「到顶了，该补了」。 */
const TOP_THRESHOLD = 60;
/** 距底多少像素以内算「还在跟着她走」。 */
const BOTTOM_STICK_PX = 80;

export interface TranscriptWindow<T> {
  scrollRef: RefObject<HTMLDivElement>;
  visible: T[];
  hasMore: boolean;
  onScroll: UIEventHandler<HTMLDivElement>;
  /** 贴到底部，并且重新开始跟随新内容。 */
  scrollToBottom: () => void;
}

export function useTranscriptWindow<T>(
  items: T[],
  resetKey: string,
  follow?: unknown,
): TranscriptWindow<T> {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);
  // 用户往上翻就松手，回到附近又自动跟上。
  const stickRef = useRef(true);
  // prepend 之前的滚动位置，等 DOM 长高了再原样还回去。
  const prependRef = useRef<{ height: number; top: number } | null>(null);
  const growingRef = useRef(false);

  const total = items.length;
  const hasMore = total > visibleCount;
  const visible = useMemo(
    () => (total > visibleCount ? items.slice(total - visibleCount) : items),
    [items, total, visibleCount],
  );

  const scrollToBottom = useCallback(() => {
    const node = scrollRef.current;
    if (!node) return;
    stickRef.current = true;
    node.scrollTop = node.scrollHeight;
  }, []);

  const grow = useCallback(() => {
    const node = scrollRef.current;
    if (!node || growingRef.current || total <= visibleCount) return;
    growingRef.current = true;
    prependRef.current = { height: node.scrollHeight, top: node.scrollTop };
    // 往前补一批就说明人不在这儿了，别把他拽回底部。
    stickRef.current = false;
    setVisibleCount((count) => count + PAGE_SIZE);
  }, [total, visibleCount]);

  // 换角色或换线程：窗口收回一页，重新贴底。
  useEffect(() => {
    setVisibleCount(PAGE_SIZE);
    stickRef.current = true;
    prependRef.current = null;
    growingRef.current = false;
  }, [resetKey]);

  // 长高之后把视觉位置还回去，否则整屏会猛地往上蹿一截。
  useLayoutEffect(() => {
    const node = scrollRef.current;
    const pending = prependRef.current;
    if (!node || !pending) return;
    node.scrollTop = node.scrollHeight - pending.height + pending.top;
    prependRef.current = null;
    growingRef.current = false;
  }, [visibleCount]);

  // 新内容到达时贴底，除非用户正在往上翻。
  useEffect(() => {
    if (!stickRef.current) return;
    scrollToBottom();
    // 图片、字体这些晚到的高度变化不会触发我们的依赖，补一帧再贴一次。
    const raf = requestAnimationFrame(() => {
      if (stickRef.current) scrollToBottom();
    });
    return () => cancelAnimationFrame(raf);
  }, [items, follow, scrollToBottom]);

  // 窗口比视口还矮时滚动事件根本不会来，得自己补，否则会卡在
  // 「明明有更早的，却再也出不来」。
  useEffect(() => {
    const node = scrollRef.current;
    if (!node || !hasMore || growingRef.current) return;
    if (node.scrollHeight <= node.clientHeight + 1) grow();
  }, [hasMore, visible, grow]);

  const onScroll: UIEventHandler<HTMLDivElement> = useCallback(
    (event) => {
      const node = event.currentTarget;
      const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
      stickRef.current = distance < BOTTOM_STICK_PX;
      if (node.scrollTop <= TOP_THRESHOLD) grow();
    },
    [grow],
  );

  return { scrollRef, visible, hasMore, onScroll, scrollToBottom };
}
