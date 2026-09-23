// 生成绑定的唯一入口。
//
// 把深层的生成路径集中在这里,其余模块统一从 `shared/repositories` 取用,
// 组件永不直接 import 生成物。
import * as App from "../../../bindings/github.com/hitk/hitk/internal/service/appservice";
import * as Chat from "../../../bindings/github.com/hitk/hitk/internal/service/chatservice";
import * as KB from "../../../bindings/github.com/hitk/hitk/internal/service/kbservice";
import * as Model from "../../../bindings/github.com/hitk/hitk/internal/service/modelservice";
import * as Settings from "../../../bindings/github.com/hitk/hitk/internal/service/settingsservice";
import * as Runtime from "../../../bindings/github.com/hitk/hitk/internal/service/runtimeservice";

export { App, Chat, KB, Model, Runtime, Settings };

export type {
  RuntimeStatus,
  SkillEnvironmentStatus,
} from "../../../bindings/github.com/hitk/hitk/internal/skillruntime/models";

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
