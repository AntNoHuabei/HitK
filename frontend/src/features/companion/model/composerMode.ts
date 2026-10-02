// 导演模式的开关。
//
// 模式记在 localStorage 里，但按角色绑定：切到别人身上时必须回到「对她说话」，
// 否则很容易把一条导演指令写进错的角色世界里。
import { useCallback, useEffect, useState } from "react";

export type ComposerMode = "chat" | "director";

const MODE_KEY = "hitk.companion.composer.mode.v1";

export function readComposerMode(characterId: string): ComposerMode {
  if (!characterId) return "chat";
  try {
    const raw = localStorage.getItem(MODE_KEY);
    if (!raw) return "chat";
    const stored = JSON.parse(raw) as { characterId?: string; mode?: string };
    if (stored?.characterId === characterId && stored.mode === "director") return "director";
  } catch {
    // 读不出来就当没记过，默认回到说话模式。
  }
  return "chat";
}

export interface ComposerModeController {
  mode: ComposerMode;
  setMode: (next: ComposerMode) => void;
}

export function useComposerMode(characterId: string): ComposerModeController {
  const [mode, setModeState] = useState<ComposerMode>(() => readComposerMode(characterId));

  // 换人即复位。旧角色那儿的导演意图不该跟着飘到新角色身上。
  useEffect(() => {
    setModeState(readComposerMode(characterId));
  }, [characterId]);

  const setMode = useCallback(
    (next: ComposerMode) => {
      setModeState(next);
      try {
        localStorage.setItem(MODE_KEY, JSON.stringify({ characterId, mode: next }));
      } catch {
        // 存不下就用当前这一次，不影响写入世界。
      }
    },
    [characterId],
  );

  return { mode, setMode };
}
