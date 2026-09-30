// 每个角色自己的模型控制。
//
// useModelControls 读写的是"模式默认模型",所有陪伴角色共用一个。多角色下这
// 样不行:给小晴换模型不能把知野也换掉。所以这里把 provider/model 存进角色
// Profile,并返回与 useModelControls 同构的 shape,ModelControl 因此不用改。
import { useCallback, useEffect, useMemo, useState } from "react";
import { errorText, modelRepository } from "../../../shared/repositories";
import type { CompanionProfile } from "../../../shared/repositories";
import type {
  ModeDefaultLite,
  ModelOptionLite,
  ProviderLite,
} from "../../../shared/types/settings";

export interface CharacterModelControls {
  providers: ProviderLite[];
  options: ModelOptionLite[];
  active: ModeDefaultLite | null;
  activeLabel: string;
  contextWindow: number;
  reasoningOptions: string[];
  /** 生效中的思考等级:profile 的值不被当前模型支持时已回落到可用项。 */
  reasoningEffort: string;
  hasUsableModel: boolean;
  /** 角色 pin 指向的模型已不可用,当前实际跟随模式默认。 */
  pinStale: boolean;
  loading: boolean;
  error: string;
  reload: () => Promise<void>;
  switchModel: (provider: string, model: string) => Promise<void>;
}

function findOption(
  options: ModelOptionLite[],
  provider: string,
  model: string,
): ModelOptionLite | undefined {
  return options.find((option) => option.provider === provider && option.model === model);
}

export function useCharacterModel(
  profile: CompanionProfile | null,
  isActive: boolean,
  /** 省略时只读:用于侧栏这类只需要解析结果、不改模型的场景。 */
  onSave?: (profile: CompanionProfile) => Promise<CompanionProfile>,
): CharacterModelControls {
  const [providers, setProviders] = useState<ProviderLite[]>([]);
  const [options, setOptions] = useState<ModelOptionLite[]>([]);
  const [modeDefault, setModeDefault] = useState<ModeDefaultLite | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const reload = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [nextProviders, nextOptions, nextDefaults] = await Promise.all([
        modelRepository.providers(),
        modelRepository.modelOptions(),
        modelRepository.modeDefaults(),
      ]);
      setProviders(nextProviders);
      setOptions(nextOptions);
      setModeDefault(nextDefaults.companion ?? null);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isActive) void reload();
  }, [isActive, reload]);

  // 解析顺序:角色 pin 且在启用列表里 → 模式默认且在列表里 → 第一个可用模型。
  const active = useMemo<ModeDefaultLite | null>(() => {
    if (options.length === 0) return null;
    if (profile?.provider && profile.model && findOption(options, profile.provider, profile.model)) {
      return { provider: profile.provider, model: profile.model };
    }
    if (modeDefault?.provider && modeDefault.model && findOption(options, modeDefault.provider, modeDefault.model)) {
      return modeDefault;
    }
    return { provider: options[0].provider, model: options[0].model };
  }, [modeDefault, options, profile]);

  const pinStale = Boolean(
    profile?.provider && profile.model && !findOption(options, profile.provider, profile.model),
  );

  const activeOption = useMemo(
    () => (active ? findOption(options, active.provider, active.model) : undefined),
    [active, options],
  );

  const activeLabel = activeOption
    ? activeOption.label || activeOption.model
    : active
      ? active.model
      : "未配置模型";

  const contextWindow = activeOption?.context ?? 0;
  const reasoningOptions = activeOption?.reasoning ?? [];

  const reasoningEffort = useMemo(() => {
    const pinned = profile?.reasoningEffort ?? "";
    if (reasoningOptions.length === 0) return "off";
    if (pinned && reasoningOptions.includes(pinned)) return pinned;
    return reasoningOptions[0];
  }, [profile?.reasoningEffort, reasoningOptions]);

  const switchModel = useCallback(
    async (provider: string, model: string) => {
      if (!profile || !onSave) return;
      const nextOption = findOption(options, provider, model);
      const nextReasoning = nextOption?.reasoning ?? [];
      const current = profile.reasoningEffort ?? "";
      // 换模型后原思考等级可能不被支持,直接落回可用值并随本次保存固化。
      const nextEffort =
        current && nextReasoning.includes(current)
          ? current
          : nextReasoning.length > 0
            ? nextReasoning[0]
            : "off";
      await onSave({ ...profile, provider, model, reasoningEffort: nextEffort });
    },
    [onSave, options, profile],
  );

  return {
    providers,
    options,
    active,
    activeLabel,
    contextWindow,
    reasoningOptions,
    reasoningEffort,
    hasUsableModel: options.length > 0,
    pinStale,
    loading,
    error,
    reload,
    switchModel,
  };
}
