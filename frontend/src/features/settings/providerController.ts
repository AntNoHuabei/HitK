// 模型服务商页面的控制器。
//
// 服务商与其启用模型由后端一次返回,这里只负责在每次写操作后重新拉取,让界面
// 永远显示后端认可的状态,而不是本地推测的状态。
import { useCallback, useEffect, useMemo, useState } from "react";
import { modelRepository } from "../../shared/repositories";
import type { CatalogProviderLite, ProviderLite } from "../../shared/types/settings";

export function useProviderController() {
  const [providers, setProviders] = useState<ProviderLite[]>([]);
  const [catalog, setCatalog] = useState<CatalogProviderLite[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [nextProviders, nextCatalog] = await Promise.all([
        modelRepository.providers(),
        modelRepository.catalog(),
      ]);
      setProviders(nextProviders);
      setCatalog(nextCatalog);
    } catch (cause) {
      setError(cause);
      throw cause;
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload().catch(() => undefined);
  }, [reload]);

  /** catalogFor 返回该服务商对应的内置目录项(自定义服务商没有)。 */
  const catalogFor = useCallback(
    (provider: ProviderLite): CatalogProviderLite | undefined =>
      catalog.find((entry) => entry.kind === provider.kind),
    [catalog],
  );

  const deleteProvider = useCallback(
    async (id: string) => {
      await modelRepository.deleteProvider(id);
      await reload();
    },
    [reload],
  );

  const setCurrent = useCallback(
    async (providerId: string, model: string) => {
      await modelRepository.setProviderModel(providerId, model);
      await reload();
    },
    [reload],
  );

  const enableModel = useCallback(
    async (providerId: string, model: string, label: string, custom: boolean) => {
      await modelRepository.enableModel(providerId, model, label, custom);
      await reload();
    },
    [reload],
  );

  const disableModel = useCallback(
    async (providerId: string, model: string) => {
      await modelRepository.disableModel(providerId, model);
      await reload();
    },
    [reload],
  );

  const setVision = useCallback(
    async (providerId: string, model: string, vision: boolean) => {
      await modelRepository.setModelVision(providerId, model, vision);
      await reload();
    },
    [reload],
  );

  /** 自定义服务商没有目录兜底,列表为空时需要提示用户去"编辑"里获取。 */
  const isEmpty = useMemo(() => !loading && providers.length === 0, [loading, providers.length]);

  return {
    providers,
    catalog,
    loading,
    error,
    isEmpty,
    catalogFor,
    reload,
    deleteProvider,
    setCurrent,
    enableModel,
    disableModel,
    setVision,
  };
}
