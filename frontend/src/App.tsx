// 应用入口。
//
// 这里只做「启动时把持久化的模式恢复进外壳状态」这一件跨模块的事,其余交给
// MainLayout 路由。
import { useEffect } from "react";
import { settingsRepository } from "./shared/repositories";
import { useShellStore } from "./features/shell/shellStore";
import MainLayout from "./views/MainLayout";

export default function App() {
  useEffect(() => {
    settingsRepository
      .get()
      .then((payload) => {
        if (payload.mode === "work" || payload.mode === "coding") {
          useShellStore.getState().setMode(payload.mode);
        }
      })
      // 拉取失败就用默认模式,不该因为设置读不到而白屏。
      .catch(() => undefined);
  }, []);

  return <MainLayout />;
}
