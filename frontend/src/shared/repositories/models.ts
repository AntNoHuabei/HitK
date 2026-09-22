// 模型服务商后端调用。
//
// 这一层负责把生成的视图类型转成 UI 的窄类型,组件不接触生成物。
import { Model } from "./bindings";
import { list, text } from "./helpers";
import type {
  CatalogModelLite,
  CatalogProviderLite,
  DiscoveredModelLite,
  ModeDefaultsLite,
  ModelOptionLite,
  ProviderInputLite,
  ProviderLite,
  ProviderModelLite,
  TestResultLite,
} from "../types/settings";
import type { DiscoveredModelView, ProviderModelView, ProviderView } from "./bindings";

function toModel(input: ProviderModelView): ProviderModelLite {
  return {
    model: input.model,
    label: input.label,
    custom: input.custom,
    reasoning: input.reasoning ?? false,
    vision: input.vision ?? false,
    tools: input.tools ?? false,
    context: input.context ?? 0,
  };
}

function toProvider(input: ProviderView): ProviderLite {
  return {
    id: input.id,
    name: input.name,
    kind: input.kind,
    baseUrl: input.baseUrl,
    model: input.model,
    region: input.region,
    custom: input.custom,
    ready: input.ready,
    hasKey: input.hasKey,
    maskedKey: text(input.maskedKey),
    models: list(input.models).map(toModel),
  };
}

function toInput(input: ProviderInputLite) {
  return {
    id: input.id,
    catalogId: input.catalogId,
    name: input.name,
    kind: input.kind,
    baseUrl: input.baseUrl,
    apiKey: input.apiKey,
    model: input.model,
    models: input.models.map<ProviderModelView>((m) => ({
      model: m.model,
      label: m.label,
      custom: m.custom,
      reasoning: m.reasoning,
      vision: m.vision,
      tools: m.tools,
      context: m.context,
    })),
  };
}

function toCatalog(input: {
  kind: string;
  name: string;
  label: string;
  baseUrl: string;
  region: string;
  envKey?: string;
  docsUrl?: string;
  apiKeyUrl?: string;
  note?: string;
  models: {
    id: string;
    label: string;
    context?: number;
    reasoning?: boolean;
    vision?: boolean;
    tools?: boolean;
    embedding?: boolean;
  }[] | null;
}): CatalogProviderLite {
  return {
    kind: input.kind,
    name: input.name,
    label: input.label,
    baseUrl: input.baseUrl,
    region: input.region,
    envKey: text(input.envKey),
    docsUrl: text(input.docsUrl),
    apiKeyUrl: text(input.apiKeyUrl),
    note: text(input.note),
    models: list(input.models).map<CatalogModelLite>((m) => ({
      id: m.id,
      label: m.label,
      context: m.context ?? 0,
      reasoning: m.reasoning ?? false,
      vision: m.vision ?? false,
      tools: m.tools ?? false,
      embedding: m.embedding ?? false,
    })),
  };
}

function toDiscovered(input: DiscoveredModelView): DiscoveredModelLite {
  return {
    id: input.id,
    label: input.label,
    reasoning: input.reasoning ?? false,
    vision: input.vision ?? false,
    tools: input.tools ?? false,
    context: input.context ?? 0,
  };
}

export const modelRepository = {
  catalog: async (): Promise<CatalogProviderLite[]> =>
    list(await Model.Catalog()).map(toCatalog),

  providers: async (): Promise<ProviderLite[]> =>
    list(await Model.Providers()).map(toProvider),

  saveProvider: async (input: ProviderInputLite): Promise<ProviderLite> =>
    toProvider(await Model.SaveProvider(toInput(input))),

  deleteProvider: (id: string): Promise<void> => Model.DeleteProvider(id),

  setProviderModel: (providerId: string, model: string): Promise<void> =>
    Model.SetProviderModel(providerId, model),

  enableModel: (
    providerId: string,
    model: string,
    label: string,
    custom: boolean,
  ): Promise<void> => Model.EnableModel(providerId, model, label, custom),

  disableModel: (providerId: string, model: string): Promise<void> =>
    Model.DisableModel(providerId, model),

  setModelVision: (providerId: string, model: string, vision: boolean): Promise<void> =>
    Model.SetModelVision(providerId, model, vision),

  discoverModels: async (input: ProviderInputLite): Promise<DiscoveredModelLite[]> =>
    list(await Model.DiscoverModels(toInput(input))).map(toDiscovered),

  testProvider: async (input: ProviderInputLite): Promise<TestResultLite> => {
    const result = await Model.TestProvider(toInput(input));
    return {
      ok: result.ok,
      message: result.message,
      latencyMs: result.latencyMs,
      reply: text(result.reply),
      model: text(result.model),
    };
  },

  modelOptions: async (): Promise<ModelOptionLite[]> =>
    list(await Model.ModelOptions()).map((option) => ({
      provider: option.provider,
      providerName: option.providerName,
      model: option.model,
      label: option.label,
      vision: option.vision ?? false,
		tools: option.tools ?? false,
		reasoning: list(option.reasoning),
      context: option.context ?? 0,
    })),

  modeDefaults: async (): Promise<ModeDefaultsLite> => {
    const raw = await Model.ModeDefaults();
    return {
      work: { provider: raw.work.provider, model: raw.work.model },
      coding: { provider: raw.coding.provider, model: raw.coding.model },
    };
  },

  setModeDefault: (mode: string, providerId: string, model: string): Promise<void> =>
    Model.SetModeDefault(mode, providerId, model),

  getPreference: (key: string): Promise<string> => Model.GetPreference(key),

  setPreference: (key: string, value: string): Promise<void> =>
    Model.SetPreference(key, value),
};
