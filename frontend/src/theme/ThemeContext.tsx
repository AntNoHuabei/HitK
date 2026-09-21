import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { App as AntApp, ConfigProvider, theme as antdTheme } from "antd";
import type { ThemeConfig } from "antd";
import zhCN from "antd/locale/zh_CN";
import { modelRepository } from "../shared/repositories";
import { DEFAULT_THEME_ID, HITK_THEMES, themeById } from "./themes";
import type { HitKTheme } from "./themes";

const THEME_STORAGE_KEY = "hitk.theme";
const PREFERENCE_KEY = "theme";

/** 本地缓存让首帧就是正确皮肤,避免启动时闪一下默认主题。 */
function readCachedThemeId(): string {
  try {
    return themeById(window.localStorage.getItem(THEME_STORAGE_KEY)).id;
  } catch {
    return DEFAULT_THEME_ID;
  }
}

function cacheThemeId(id: string) {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, id);
  } catch {
    // 受限环境里 WebView 存储可能不可用。
  }
}

interface ThemeCtxValue {
  themeId: string;
  theme: HitKTheme;
  setTheme: (id: string) => Promise<void>;
}

const ThemeCtx = createContext<ThemeCtxValue>({
  themeId: DEFAULT_THEME_ID,
  theme: themeById(DEFAULT_THEME_ID),
  setTheme: async () => undefined,
});

/** useHitKTheme 读取当前主题并切换。 */
export function useHitKTheme(): ThemeCtxValue {
  return useContext(ThemeCtx);
}

/**
 * 全局主题 Provider:
 *  1. 启动时读取偏好项 theme(默认 light)并持久化;
 *  2. 把主题的 CSS 变量以 --hitk-* 注入 <html>,驱动自定义样式的背景/边框;
 *  3. 通过 ConfigProvider 把 antd token 应用到整棵组件树;
 *  4. 提供 setTheme(id) 热切换并落盘。
 */
export default function ThemeProvider({ children }: { children: ReactNode }) {
  const [themeId, setThemeId] = useState<string>(readCachedThemeId);
  // 用户在启动恢复完成前就点了主题时,用版本号让迟到的响应作废。
  const selectionVersion = useRef(0);
  const theme = useMemo(() => themeById(themeId), [themeId]);

  useEffect(() => {
    const cachedId = readCachedThemeId();
    const version = selectionVersion.current;
    modelRepository
      .getPreference(PREFERENCE_KEY)
      .then((stored) => {
        if (selectionVersion.current !== version) return;
        const id = themeById(stored || cachedId).id;
        setThemeId(id);
        cacheThemeId(id);
        if (!stored) void modelRepository.setPreference(PREFERENCE_KEY, id).catch(() => undefined);
      })
      .catch(() => undefined);
  }, []);

  useEffect(() => {
    const root = document.documentElement.style;
    for (const [key, value] of Object.entries(theme.vars)) {
      root.setProperty(`--hitk-${key}`, value);
    }
    document.documentElement.style.colorScheme = theme.dark ? "dark" : "light";
  }, [theme]);

  const setTheme = useCallback(async (id: string) => {
    const next = themeById(id);
    selectionVersion.current += 1;
    setThemeId(next.id);
    cacheThemeId(next.id);
    try {
      await modelRepository.setPreference(PREFERENCE_KEY, next.id);
    } catch {
      // 持久化失败不阻塞切换。
    }
  }, []);

  const config = useMemo<ThemeConfig>(() => {
    const algorithm = theme.dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm;
    return { algorithm, token: theme.token as unknown as ThemeConfig["token"] };
  }, [theme]);

  return (
    <ThemeCtx.Provider value={{ themeId, theme, setTheme }}>
      <ConfigProvider locale={zhCN} theme={config}>
        <AntApp>{children}</AntApp>
      </ConfigProvider>
    </ThemeCtx.Provider>
  );
}

export { HITK_THEMES };
