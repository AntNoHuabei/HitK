// 仓储层的唯一出口。
//
// 组件只从这里取数据:每个 repository 负责调用生成绑定并把结果归一化成 UI
// 类型,组件因此既不需要知道绑定的路径,也不需要防 null。
export { appRepository } from "./app";
export { chatRepository } from "./chat";
export { companionRepository } from "./companion";
export { knowledgeRepository } from "./knowledge";
export { modelRepository } from "./models";
export { runtimeRepository } from "./runtime";
export { settingsRepository } from "./settings";

export { errorText, list, stringMap, text, toError } from "./helpers";

export type {
  ApprovalRequest,
	Attachment,
	Artifact,
  Info,
  Item,
  ProtocolEvent,
  Reference,
  Thread,
  ThreadSummary,
  TodoEntry,
  ToolCall,
  Usage,
  CompanionCharacter,
  CompanionDirective,
  CompanionDirectiveChange,
  CompanionEvent,
  CompanionEventKind,
  CompanionDerivedState,
  CompanionMemory,
  CompanionMetricView,
  CompanionOverview,
  CompanionProfile,
  CompanionState,
  CompanionTurnBrief,
  CompanionTurnTaskView,
  CompanionWeather,
  LifeEvent,
  RuntimeStatus,
  SkillEnvironmentStatus,
  ResolvedPlan,
  SkillSummary,
  InstallRequest,
  SkillHubFeed,
  SkillHubEntry,
  DependencyCandidate,
} from "./bindings";
export {
  ApprovalPolicy as ApprovalPolicyEnum,
  Consciousness,
  EventKind,
  ItemStatus,
  ItemType,
  Mode as ModeEnum,
  OpKind,
  SkillKind,
  SourceKind,
  TrustLevel,
  TurnTaskStatus,
} from "./bindings";
