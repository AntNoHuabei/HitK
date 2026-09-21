// 应用级设置(审批策略、步数上限、检索嵌入模型)。
import { Settings } from "./bindings";
import { list, stringMap, text } from "./helpers";
import type { AppSettingsLite } from "../types/settings";

export const settingsRepository = {
  get: async (): Promise<AppSettingsLite> => {
    const raw = await Settings.Get();
    return {
      mode: raw.mode,
      policy: raw.policy,
      maxSteps: raw.maxSteps,
      contextLimit: raw.contextLimit,
      embeddingVendor: raw.embeddingVendor,
      embeddingModel: raw.embeddingModel,
      workspace: raw.workspace,
      recentWorkspaces: list(raw.recentWorkspaces),
      onboarded: raw.onboarded,
      kbEmbedder: text(raw.kbEmbedder),
      versions: stringMap(raw.versions),
    };
  },

  saveOptions: (
    maxSteps: number,
    contextLimit: number,
    embeddingVendor: string,
    embeddingModel: string,
  ): Promise<void> => Settings.SaveOptions(maxSteps, contextLimit, embeddingVendor, embeddingModel),

  completeOnboarding: (): Promise<void> => Settings.CompleteOnboard(),
};
