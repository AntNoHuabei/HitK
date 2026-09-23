// 应用级后端调用(窗口、工作区、审批策略)。
import { App } from "./bindings";
import type { Info } from "./bindings";

export const appRepository = {
  info: (): Promise<Info> => App.GetInfo(),

  pickDirectory: (title = "选择目录"): Promise<string> => App.PickDirectory(title),

  /**
   * pickFiles 返回选中的文件。
   *
   * 取消对话框不算错误,所以这里吞掉拒绝并把结果兜成数组——调用方只需判断
   * 长度。
   */
  pickFiles: async (title = "选择文件"): Promise<string[]> => {
    try {
      return (await App.PickFiles(title)) ?? [];
    } catch {
      return [];
    }
  },

  setWorkspace: (dir: string): Promise<void> => App.SetWorkspace(dir),

  setMode: (mode: string): Promise<void> => App.SetMode(mode),

  setPolicy: (policy: string): Promise<void> => App.SetPolicy(policy),

  reveal: (path: string): Promise<void> => App.RevealInFileManager(path),

  revealArtifact: (path: string): Promise<void> => App.RevealArtifact(path),

  previewArtifact: (path: string) => App.PreviewArtifact(path),
};
