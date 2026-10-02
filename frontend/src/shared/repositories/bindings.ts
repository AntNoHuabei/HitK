// 生成绑定的唯一入口。
//
// 把深层的生成路径集中在这里,其余模块统一从 `shared/repositories` 取用,
// 组件永不直接 import 生成物。
import * as App from "../../../bindings/github.com/hitk/hitk/internal/service/appservice";
import * as Chat from "../../../bindings/github.com/hitk/hitk/internal/service/chatservice";
import * as Companion from "../../../bindings/github.com/hitk/hitk/internal/service/companionservice";
import * as KB from "../../../bindings/github.com/hitk/hitk/internal/service/kbservice";
import * as Model from "../../../bindings/github.com/hitk/hitk/internal/service/modelservice";
import * as Settings from "../../../bindings/github.com/hitk/hitk/internal/service/settingsservice";
import * as Runtime from "../../../bindings/github.com/hitk/hitk/internal/service/runtimeservice";

export { App, Chat, Companion, KB, Model, Runtime, Settings };

export type {
  CharacterSummary as CompanionCharacter,
  DirectorDirective as CompanionDirective,
  DirectiveChange as CompanionDirectiveChange,
  DerivedState as CompanionDerivedState,
  Event as CompanionEvent,
  EventKind as CompanionEventKind,
  LifeEvent,
  Memory as CompanionMemory,
  MetricView as CompanionMetricView,
  Overview as CompanionOverview,
  Profile as CompanionProfile,
  State as CompanionState,
  TurnBrief as CompanionTurnBrief,
  TurnTaskView as CompanionTurnTaskView,
  WeatherSnapshot as CompanionWeather,
} from "../../../bindings/github.com/hitk/hitk/internal/companion/models";

// 值是运行时枚举，所以单独走值导出，组件靠它判断醒还是睡、这一轮走到哪一步。
export {
  Consciousness,
  TurnTaskStatus,
} from "../../../bindings/github.com/hitk/hitk/internal/companion/models";

export type {
  RuntimeProgress,
  RuntimeStatus,
  SkillEnvironmentStatus,
} from "../../../bindings/github.com/hitk/hitk/internal/skillruntime/models";

export type {
  AnalysisPlan,
  DependencyCandidate,
  ResolvedPlan,
  SkillSummary,
} from "../../../bindings/github.com/hitk/hitk/internal/skillmodel/models";

export {
  SkillKind,
  SourceKind,
  TrustLevel,
} from "../../../bindings/github.com/hitk/hitk/internal/skillmodel/models";

export type {
  InstallRequest,
  SkillHubEntry,
  SkillHubFeed,
} from "../../../bindings/github.com/hitk/hitk/internal/skillcatalog/models";

// 服务层模型
export type {
  CatalogModelView,
  CatalogProviderView,
  DiscoveredModelView,
  ImportResult,
  Info,
  ModeDefaultView,
  ModeDefaultsView,
  ModelOptionView,
  ProviderInput,
  ProviderModelView,
  ProviderView,
  SearchHitView,
  Settings as AppSettingsPayload,
  TestResult,
} from "../../../bindings/github.com/hitk/hitk/internal/service/models";

// 知识库模型
export type {
  Collection,
  Document,
  Stats,
} from "../../../bindings/github.com/hitk/hitk/internal/kb/models";

// 协议模型
export type {
  ApprovalRequest,
	Attachment,
	Artifact,
  Event as ProtocolEvent,
  Item,
  Reference,
  Thread,
  ThreadSummary,
  TodoEntry,
  ToolCall,
  Usage,
} from "../../../bindings/github.com/hitk/hitk/internal/protocol/models";

export {
  ApprovalPolicy,
  EventKind,
  ItemStatus,
  ItemType,
  Mode,
  OpKind,
} from "../../../bindings/github.com/hitk/hitk/internal/protocol/models";
