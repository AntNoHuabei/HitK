// 对话区的模型控制。
//
// 模式(Work / Coding)各自记住一个默认模型,切换模式时把该模式的默认值套用到
// 当前会话上,这样"用便宜模型写文档、用强模型改代码"可以并存。
import { useCallback, useEffect, useMemo, useState } from "react";
import { errorText, modelRepository } from "../../../shared/repositories";
import type { ModeDefaultsLite, ModelOptionLite, ProviderLite } from "../../../shared/types/settings";
import type { AgentMode } from "../../../shared/types/chat";

export function useModelControls(mode: AgentMode, isActive: boolean, refreshKey: string) {
  const [providers, setProviders] = useState<ProviderLite[]>([]);
  const [options, setOptions] = useState<ModelOptionLite[]>([]);
  const [defaults, setDefaults] = useState<ModeDefaultsLite | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [nextProviders, nextOptions, nextDefaults] = await Promise.all([
        modelRepository.providers(),
        modelRepository.modelOptions(),
        modelRepository.modeDefaults(),
      ]);
      setProviders(nextProviders);
      setOptions(nextOptions);
      setDefaults(nextDefaults);
    } catch (cause) {
      setError(cause);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isActive) void reload();
  }, [isActive, refreshKey, reload]);

  /** 当前模式只可指向实际启用的模型；配置文件中的默认值不是可用性证明。 */
  const active = useMemo(() => {
    if (!defaults || options.length === 0) return null;
    const choice = defaults[mode];
    if (choice.provider && choice.model && options.some((option) => option.provider === choice.provider && option.model === choice.model)) return choice;
    return { provider: options[0].provider, model: options[0].model };
  }, [defaults, mode, options]);

  const activeLabel = useMemo(() => {
    if (!active) return "未配置模型";
    const found = options.find(
      (option) => option.provider === active.provider && option.model === active.model,
    );
    if (!found) return active.model;
    return found.label || found.model;
  }, [active, options]);

  const contextWindow = useMemo(() => {
    if (!active) return 0;
    const found = options.find(
      (option) => option.provider === active.provider && option.model === active.model,
    );
    return found?.context ?? 0;
  }, [active, options]);

  const reasoningOptions = useMemo(() => {
    if (!active) return [];
    return options.find((option) => option.provider === active.provider && option.model === active.model)?.reasoning ?? [];
  }, [active, options]);

  /** switchModel 把某个模型设为当前模式的默认值。 */
  const switchModel = useCallback(
    async (provider: string, model: string) => {
      await modelRepository.setModeDefault(mode, provider, model);
      await reload();
    },
    [mode, reload],
  );

  const hasUsableModel = options.length > 0;

  return {
    providers,
    options,
    active,
    activeLabel,
    contextWindow,
		reasoningOptions,
    hasUsableModel,
    loading,
    error: error ? errorText(error) : "",
    reload,
    switchModel,
  };
}
