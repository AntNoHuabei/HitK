// 模型服务商与偏好的领域类型。
//
// 这些是 UI 自己使用的窄类型,由 repositories 从生成绑定转换而来,组件不直接
// 接触生成的 class。
export interface ProviderModelLite {
  model: string;
  label: string;
  custom: boolean;
  reasoning: boolean;
  vision: boolean;
  tools: boolean;
  context: number;
}

export interface ProviderLite {
  id: string;
  name: string;
  /** 内置服务商为厂商 id,自定义为自定义入口。 */
  kind: string;
  baseUrl: string;
  /** 该服务商当前选中的模型。 */
  model: string;
  region: string;
  custom: boolean;
  ready: boolean;
  hasKey: boolean;
  maskedKey: string;
  models: ProviderModelLite[];
}

export interface CatalogModelLite {
  id: string;
  label: string;
  context: number;
  reasoning: boolean;
  vision: boolean;
  tools: boolean;
  embedding: boolean;
}

export interface CatalogProviderLite {
  kind: string;
  name: string;
  label: string;
  baseUrl: string;
  region: string;
  envKey: string;
  docsUrl: string;
  apiKeyUrl: string;
  note: string;
  models: CatalogModelLite[];
}

export interface ProviderInputLite {
  id: string;
  /** 内置服务商的厂商 id;自定义服务商留空。 */
  catalogId: string;
  name: string;
  kind: string;
  baseUrl: string;
  apiKey: string;
  model: string;
  models: ProviderModelLite[];
}

export interface DiscoveredModelLite {
  id: string;
  label: string;
  reasoning: boolean;
  vision: boolean;
  tools: boolean;
  context: number;
}

export interface ModelOptionLite {
  provider: string;
  providerName: string;
  model: string;
  label: string;
  vision: boolean;
  tools: boolean;
  /** Only adapter-verified effort values are exposed. */
	reasoning: string[];
  context: number;
}

export interface ModeDefaultLite {
  provider: string;
  model: string;
}

export interface ModeDefaultsLite {
  work: ModeDefaultLite;
  coding: ModeDefaultLite;
  companion: ModeDefaultLite;
}

export interface TestResultLite {
  ok: boolean;
  message: string;
  latencyMs: number;
  reply: string;
  model: string;
}

/** 应用级设置(与模型管理分开)。 */
export interface AppSettingsLite {
  mode: string;
  policy: string;
  maxSteps: number;
  contextLimit: number;
  embeddingVendor: string;
  embeddingModel: string;
  workspace: string;
  recentWorkspaces: string[];
  onboarded: boolean;
  kbEmbedder: string;
  versions: Record<string, string>;
}
