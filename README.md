# HitK

**Agentic Search 知识库桌面端** —— 让模型自己去检索、定位、引用，而不是把资料一次性塞进上下文。

Wails v3 (Go) + React 18 / Ant Design 5。同一套界面兼顾 **Work**（面向资料与知识库）与 **Coding**（面向代码仓库与命令执行）两种工作方式——两者共用同一块对话区域，只在左上角切换。内置 10 套主题，设置页采用左分区 / 右内容的双栏布局。

---

## 1. 它解决什么

传统「知识库问答」是 RAG 一步流：切块 → 向量召回 → 拼进 prompt → 生成。检索质量由一次召回的运气决定，答案也无法回溯到具体出处。

HitK 走的是 Agentic Search：检索本身是**模型可以反复调用的工具**。

```
用户提问
   ↓
模型决定：先看看有哪些知识库         → kb_collections
模型决定：用 3 个改写式检索          → kb_search（混合检索，带分数）
模型决定：第 2 条命中可疑，展开上下文  → kb_open
模型决定：信息够了，组织答案并标注来源
   ↓
回答携带引用（标题 / 分块 / 可跳转原文）
```

模型能自己判断「召回不够，换个说法再搜」，也能解释「我为什么引用这一段」。这三点是 HitK 与普通 RAG 的分界。

---

## 2. 快速开始

### 依赖

| 组件 | 版本 |
| --- | --- |
| Go | 1.25+（开发环境实测 1.26.3） |
| Wails CLI | v3（`wails3`） |
| Node.js | 22+ |
| C 编译器 | Windows 用 mingw-w64，macOS 用 Xcode CLT |

### 开发运行

```bash
cd HitK
wails3 dev            # 前端热更新 + 后端自动重启
```

### 生产构建

```bash
wails3 build          # 产物在 build/bin/
# 或只构建当前平台的二进制
go build -o bin/hitk.exe .
```

`go.mod` 中的 `ignore ./build` 排除了 Wails 的移动端存根目录，因此 `go build ./...` 可以直接使用。

### 测试

```bash
go test ./internal/... -count=1          # 全量
go test ./internal/service/ -v -count=1  # 端到端：入库 → 检索 → 工具调用 → 落盘
```

---

## 3. 首次配置

启动后进入**设置 → 模型服务商**，至少配置一个服务商的 API Key。

预设 18 家服务商（挑重点）：

- **国际**：OpenAI、Anthropic Claude、Google Gemini、xAI Grok、Mistral、Groq、OpenRouter
- **国内**：DeepSeek、月之暗面 Kimi、智谱 GLM、阿里云通义千问、字节豆包、腾讯混元、百度文心千帆、MiniMax、硅基流动
- **本地 / 自建**：Ollama、自定义 OpenAI 兼容端点

**内置服务商**自带模型目录，填好 Key 之后在列表里逐条**启用**想用的模型即可，不用手工填写模型名；模型条目标注了上下文窗口、是否支持推理 / 视觉 / 工具调用，启用后选择时会自动带出对应的上下文上限。

**自定义服务商**（自建网关、代理、兼容端点）没有内置目录，保存配置时会调用其 `/models` 接口**拉取模型列表**，勾选需要的模型后再保存；拉取失败会给出原因，允许先保存、稍后重试。

每个服务商都可以单独「测试连接」，并设为 Work / Coding 各自的默认模型——两种模式互不干扰，切换模式即切换模型。

**检索向量**可选：默认使用内置的离线哈希向量（零配置、零成本、无需联网）；也可以在设置里指定一家厂商的 embedding 模型（如 `text-embedding-3-small`、`bge-m3`、`nomic-embed-text`），检索会自动切到混合模式，并在切换模型后提示重建索引。

---

## 4. 两种模式

对话和编码**不分家**：左侧栏、消息流、输入框、审批弹窗都是同一套组件，左上角切换 Work / Coding 只改变这一轮携带的 mode（默认模型、工具集、作用域、审批策略随之切换），会话历史连续保留，不会因为切换模式而另开一块区域。

| | Work | Coding |
| --- | --- | --- |
| 面向 | 资料、知识库、研究 | 代码仓库、命令执行 |
| 工具集 | kb_* 检索工具、抓网页、写知识库 | 再加读写文件、目录导航、搜索、Shell |
| 作用域 | 可绑定某个知识库 | 绑定工作区目录 |
| 默认模型 | 可单独指定 | 可单独指定 |
| 审批默认 | 只读优先 | 写操作 / 命令逐次确认 |

**审批策略**分三档：`只读`（拒绝一切写操作）、`手动`（写操作弹窗确认，可勾选「本会话不再询问」）、`自动`（不再打断）。

单轮工具步数默认 **64**（一轮 agentic 检索通常花 8–15 步），可在「设置 → 数据与导出」里调到上限 **500**。这个上限是失控保护，不是推荐值——步数越高，一轮可能消耗的 token 越多。

---

## 5. 架构

```
main.go                     装配：配置 → 容器 → 注册 5 个服务 → 开窗（无边框）
internal/
  protocol/   提交(Op) 与事件(Event) 的类型定义 —— 前后端唯一的契约
  provider/   18 家厂商接入层，统一成流式 chat + tool-calling
  kb/         知识库：解析 → 分块 → 索引 → 混合检索（BM25 + 向量）
  tools/      工具注册表：kb_* / 文件 / Shell / 网页 / 待办
  agent/      turn 循环（ReAct）：流式 → 工具 → 回灌 → 再流式
  service/    Wails 服务：App / Chat / KB / Settings / Model
  config/     配置读写与路径解析
frontend/src/
  theme/      主题定义与 ThemeContext（CSS 变量注入）
  shared/     types / repositories（服务调用封装）/ wails（事件订阅）/ lib / components
  features/   按功能拆分的模块：shell / chat / knowledge / settings
  views/      MainLayout / ChatView / SettingsView
  styles/     base / app-shell / chat / knowledge / settings / citations / permissions
```

### 前端按功能拆分

每个 feature 内部固定三层，避免「状态散落在组件里」：

```
features/chat/
  model/conversationStore.ts     zustand：事件流 → 会话状态（唯一的归约点）
  controllers/                   useChatRuntime（驱动一轮对话）/ useModelControls
  components/                    Composer / Messages / Viewport / Sidebar / ApprovalModal
```

- **shared/repositories**：所有 Wails 服务调用的唯一入口，组件不直接 import 生成的绑定。绑定是 interface 模式（`wails3 task common:generate:bindings`），Go 的 nil slice 过桥是 `null`，因此仓库层统一做空值兜底。
- **features/shell/shellStore.ts**：只管视图路由（chat / knowledge / settings），不掺业务状态。
- **事件 → 状态的归约只发生在 `conversationStore` 一处**：`shared/wails/events.ts` 订阅全局事件后分发进 store，组件只读状态、不碰协议。

### 主题系统

`theme/themes.ts` 定义 10 套主题（明亮 / 暗夜 / 护眼绿 / 极客紫 / 石墨 / 青瓷 / 朱砂 / 琥珀 / 深海 / 高对比），每套是一组 `--hitk-*` CSS 变量；`ThemeContext` 把变量注入根节点，并同步给 Ant Design 的 `ConfigProvider`，所以 antd 组件与自绘界面一起换色。选择结果作为偏好项持久化，下次启动直接还原。

### 设置页布局

左侧分区、右侧内容（仿 WorkBuddy）：**模型服务商 / 知识库 / 外观与主题 / 数据与导出 / 关于**。

### 协议：提交与事件分离

交互协议参考 codex-rs 的设计思路并做了简化：

- **Op（前端 → 后端）**：`turn.start` / `turn.interrupt` / `approval.respond`。一次提交携带本轮的 mode、模型、知识库作用域、审批策略，因此不必先改全局设置再发消息。
- **Event（后端 → 前端，单通道流）**：`thread.ready` / `turn.started` / `item.started` / `item.delta` / `item.completed` / `turn.completed` / `turn.aborted` / `approval.requested` / `token.count` / `stream.error` / `notice`。

关键约定：**界面只渲染 Item**。模型文本、推理、工具调用、错误、系统提示都是 Item，状态经 `started → delta → completed` 流转。因此「模型正在输出」「工具跑了 300ms」「这一轮被中断」在 UI 上是同一套渲染逻辑，没有特例分支。

### 审批门控由 Agent 掌握，而不是工具

写操作的安全边界不能依赖工具自觉。`agent` 在调用 mutating 工具**之前**就发起征询，用户的回答决定工具体是否运行；工具自身若也调用 `Approve`（为了展示 diff、完整命令等更细的信息），会复用同一个回答，不会二次打扰。

这带来两个性质：工具即使漏写审批代码也不会「静默放行」；一次操作对用户只产生一次打扰。

### 引用溯源

一轮对话中所有工具返回的引用会被去重、按分数排序（上限 12 条），挂到这一轮的最终回答上。用户看到的每条结论都能点回原文，而不是只有一句「依据知识库」。

---

## 6. 内置工具

| 工具 | 说明 |
| --- | --- |
| `kb_collections` | 列出知识库及其文档数量（检索前先调用，避免空搜） |
| `kb_search` | 混合检索（BM25 + 向量融合排序），支持一次传 2–4 个改写式 |
| `kb_open` | 按 chunkId 展开上下文，或查看文档目录 |
| `kb_add` | 把 URL 或纯文本写入知识库，写入即可检索 |
| `fetch_url` | 抓取网页正文并转成可读文本 |
| `todo_write` | 维护本轮任务清单，界面实时渲染进度 |
| `read_file` / `list_dir` / `glob_find` / `grep_search` | 代码仓库导航 |
| `write_file` / `edit_file` | 写文件、按片段精确替换（走审批） |
| `run_command` | 执行 Shell 命令（走审批，输出超长自动截断并标注） |

---

## 7. 数据存放

| 平台 | 路径 |
| --- | --- |
| Windows | `%APPDATA%\HitK\` |
| macOS | `~/Library/Application Support/HitK/` |
| Linux | `$XDG_CONFIG_HOME/HitK/` 或 `~/.config/HitK/` |

目录内容：`config.json`（设置与厂商凭据）、`knowledge.db`（bbolt 知识库）、`threads/*.json`（每会话一个文件，先写临时文件再重命名，崩溃不会留下半截文件）、`hitk.log`。

设置环境变量 `HITK_DATA_DIR` 可覆盖数据目录（便于多套配置并存或做本地调试）。

---

## 8. 开发约定

几条踩过坑、改代码时必须遵守的规则：

- **生成绑定必须走 Taskfile**：`wails3 task common:generate:bindings`（等价于 `-clean=true -ts -i`）。`-i` 生成 interface 而非 class，缺了它拿到的是一份「说假话的类型」，和实际产物不一致。
- **Go 的 nil slice 过桥就是 `null`**，所以 interface 模式下 slice 字段被如实标成 `T[] | null`。前端一律 `?? []` 兜底，不要试图把类型压回 `T[]`。
- **加新能力先改 `internal/protocol`**，再生成绑定；事件类型和 `registerEvent` 都要在 `main.go` 的 `init()` 里登记，否则前端拿到 `any`。
- **厂商模型改名要改两处**：`provider.Catalog` 的模型列表，加上 `provider.retiredModels` 的「退役 ID → 在售 ID」映射。只改前者，老用户 `config.json` 里的旧 ID 会一直指向已下线的模型，而默认值只在首次创建配置时生效，看起来就像"改了没反应"。
- **写操作的门控由 agent 掌握**，新增 mutating 工具只需 `Mutating() bool` 返回 true，agent 会在运行前征询；工具内调 `ctx.Approve()` 展示细节会复用同一个回答，不会二次弹窗。
- **`wails3 dev` 的等待窗口只有 5 秒**（应用启动后等 Vite），所以 dev 链路里不能再插入 `npm install` 之类的长任务。
- **`go.mod` 里的 `ignore ./build` 不要删**，否则 Wails 的移动端存根会让 `go build ./...` 失败。
- 验证完整 dev 链路用 `wails3 build DEV=true`（`wails3 dev` 的阻塞步骤，能跑完并退出，更适合自动化验证）。

---

## 9. 已知限制

- 前端为单页 React 应用，尚未做虚拟滚动；超长会话（数千条 Item）滚动会变慢。
- 离线哈希向量适合中小规模语料；语料明显变大或需要跨语言语义检索时，建议配置厂商 embedding 模型并重建索引。
- 检索索引存在本地 bbolt 中，暂不支持多设备同步。
- **内置服务商**的模型目录是内置快照，新模型需更新 `internal/provider/catalog.go` 后重新编译；自定义服务商走 `/models` 拉取，不受此限。
- 厂商**改名或下线**模型不会让已保存的选择失效：`internal/provider` 维护一份「退役 ID → 在售 ID」映射，启动时自动改写 `config.json` 里的当前模型、两种模式的默认模型与已启用列表并落盘。目录里没有的模型（用户手填的、自建网关的）原样保留，不会被改写。
