// 对话消息渲染。
//
// 界面只渲染 Item:模型文本、推理、工具调用、计划、提示、错误都是 Item,所以
// 这里是一张按 item.type 分派的表,而不是每种内容一段特例逻辑。
import { useState } from "react";
import { Button, Flex, Image, Tooltip, Typography } from "antd";
import {
  BulbOutlined,
  CheckCircleFilled,
  CloseCircleFilled,
  CopyOutlined,
  FileTextOutlined,
  LinkOutlined,
  LoadingOutlined,
  ToolOutlined,
  WarningFilled,
} from "@ant-design/icons";
import { appRepository, ItemStatus, ItemType } from "../../../shared/repositories";
import type { Artifact, Item, Reference, TodoEntry, Usage } from "../../../shared/repositories";
import { Markdown } from "../../../shared/components/Markdown";

const { Text } = Typography;

/** 单条回答最多展示的引用数,与后端一轮上限保持一致。 */
const MAX_REFS = 12;

interface MessagesProps {
  items: Item[];
  buffers: { text: Record<string, string>; reasoning: Record<string, string> };
  running: boolean;
  modelLabel: string;
  usage: Usage | null;
}

/**
 * mergeRefs 按 chunk 去重并保留最高分,再按分数降序截断。
 *
 * 检索同一轮内可能命中同一段多次,不去重会让引用列表看起来比实际依据多。
 */
function mergeRefs(refs: (Reference | null)[]): Reference[] {
  const best = new Map<string, Reference>();
  for (const ref of refs) {
    if (!ref) continue;
    const key = ref.chunkId || `${ref.docId}:${ref.snippet ?? ""}`;
    const previous = best.get(key);
    if (!previous || ref.score > previous.score) best.set(key, ref);
  }
  return [...best.values()].sort((a, b) => b.score - a.score).slice(0, MAX_REFS);
}

export function ConversationMessages({ items, buffers, running, modelLabel, usage }: MessagesProps) {
  const refs = mergeRefs(items.flatMap((item) => item.refs ?? []));
  const lastAssistantIndex = items.findLastIndex((item) => item.type === ItemType.ItemAgentMessage);

  return (
    <>
      {items.map((item, index) => (
        <ItemRow
          key={item.id}
          item={item}
          buffers={buffers}
          streaming={running && index === items.length - 1}
          modelLabel={modelLabel}
          usage={usage}
          refs={index === lastAssistantIndex ? refs : []}
        />
      ))}
    </>
  );
}

interface RowProps {
  item: Item;
  buffers: MessagesProps["buffers"];
  streaming: boolean;
  modelLabel: string;
  usage: Usage | null;
  refs: Reference[];
}

function ItemRow({ item, buffers, streaming, modelLabel, usage, refs }: RowProps) {
  switch (item.type) {
    case ItemType.ItemUserMessage:
      return (
        <Flex justify="flex-end" style={{ margin: "10px 0" }}>
          <div className="hitk-chat-user-message">
            {item.attachments?.length ? <div className="hitk-chat-attachment-strip">{item.attachments.map((attachment) => <Tooltip key={attachment.path} title={attachment.path}><span className="hitk-chat-attachment"><FileTextOutlined /><span>{attachment.name}</span></span></Tooltip>)}</div> : null}
            <span className="hitk-chat-user-message-text">{item.text}</span>
          </div>
        </Flex>
      );

    case ItemType.ItemAgentMessage: {
      const body = buffers.text[item.id] ?? item.text ?? "";
		if (!body && streaming) return null;
      return (
        <div className="hitk-chat-assistant-message">
          <Markdown>{body}</Markdown>
          {streaming && <span className="hitk-cursor" />}
          {refs.length > 0 && !streaming && <CitationRow refs={refs} />}
          {!streaming && (
            <ResponseMeta body={body} model={modelLabel} usage={usage} />
          )}
        </div>
      );
    }

    case ItemType.ItemReasoning: {
      const body = buffers.reasoning[item.id] ?? item.reasoning ?? "";
      if (!body) return null;
      return (
        <details className="hitk-agent-detail">
          <summary>
            <BulbOutlined />
            <Text type="secondary" style={{ fontSize: 13 }}>
              思考过程
            </Text>
          </summary>
          <div className="hitk-agent-reasoning">{body}</div>
        </details>
      );
    }

    case ItemType.ItemToolCall:
      return <ToolRow item={item} />;

    case ItemType.ItemPlan:
      return <PlanRow entries={item.plan ?? []} />;

    case ItemType.ItemNotice:
      return (
        <div className="hitk-agent-process">
          <Text type="secondary" style={{ fontSize: 13 }}>
            {item.text || item.error}
          </Text>
        </div>
      );

    case ItemType.ItemError:
      return (
        <div className="hitk-chat-run-error">
          <div className="hitk-chat-run-error-heading">
            <CloseCircleFilled />
            <span>生成失败</span>
          </div>
          <div className="hitk-chat-run-error-description">{item.error || item.text}</div>
        </div>
      );

    default:
      return null;
  }
}

function ToolRow({ item }: { item: Item }) {
  const tool = item.tool;
  if (!tool) return null;
  const pending = item.status === ItemStatus.StatusInProgress;
  const denied = tool.denied ?? false;
  const ok = tool.success && !denied;

  return (
    <details className="hitk-agent-detail" open={pending}>
      <summary>
        {pending ? (
          <LoadingOutlined />
        ) : denied ? (
          <WarningFilled style={{ color: "var(--hitk-muted)" }} />
        ) : ok ? (
          <CheckCircleFilled style={{ color: "var(--hitk-signal)" }} />
        ) : (
          <CloseCircleFilled style={{ color: "var(--ant-color-error, #d4380d)" }} />
        )}
        <ToolOutlined />
        <span>{tool.displayName || tool.name}</span>
        <span className="hitk-tool-status">
          {pending
            ? "执行中…"
            : denied
              ? "已拒绝"
              : `${tool.durationMs} ms${tool.truncated ? " · 已截断" : ""}`}
        </span>
      </summary>
      {tool.arguments && <div className="hitk-tool-detail">{tool.arguments}</div>}
      {tool.result && <div className="hitk-tool-detail">{tool.result}</div>}
      {item.artifacts?.length ? <ArtifactRow artifacts={item.artifacts} /> : null}
    </details>
  );
}

function ArtifactRow({ artifacts }: { artifacts: Artifact[] }) {
  return <div className="hitk-artifact-list">{artifacts.map((artifact) => <ArtifactCard key={artifact.path} artifact={artifact} />)}</div>;
}

function ArtifactCard({ artifact }: { artifact: Artifact }) {
  const [preview, setPreview] = useState<{ mime: string; content?: string; data?: string; truncated?: boolean } | null>(null);
  const [loading, setLoading] = useState(false);
  const image = preview?.data ? `data:${preview.mime};base64,${preview.data}` : "";
  const showPreview = async () => {
    if (preview || loading) return;
    setLoading(true);
    try { setPreview(await appRepository.previewArtifact(artifact.path)); } finally { setLoading(false); }
  };
  return <div className="hitk-artifact-card"><div className="hitk-artifact-heading"><FileTextOutlined /><span>{artifact.name}</span><Text type="secondary">{artifact.operation === "created" ? "新建" : "已修改"}</Text><Button type="text" size="small" onClick={() => void appRepository.reveal(artifact.path)}>打开</Button><Button type="text" size="small" onClick={() => void showPreview()}>{loading ? "加载中" : "预览"}</Button></div>{preview && <div className="hitk-artifact-preview">{image ? <Image src={image} preview /> : <pre>{preview.content}</pre>}{preview.truncated && <Text type="secondary">预览已截断</Text>}</div>}</div>;
}

function PlanRow({ entries }: { entries: TodoEntry[] }) {
  if (entries.length === 0) return null;
  return (
    <details className="hitk-agent-detail">
      <summary>
        <FileTextOutlined />
        <span>执行计划</span>
        <span className="hitk-tool-status">
          {entries.filter((entry) => entry.status === "completed").length}/{entries.length}
        </span>
      </summary>
      <div className="hitk-tool-detail">
        {entries.map((entry, index) => (
          <div key={`${entry.text}-${index}`}>
            {entry.status === "completed" ? "✓" : entry.status === "in_progress" ? "→" : "·"}{" "}
            {entry.text}
          </div>
        ))}
      </div>
    </details>
  );
}

function CitationRow({ refs }: { refs: Reference[] }) {
  return (
    <div className="hitk-chat-refs">
      <span className="hitk-chat-refs-label">依据</span>
      {refs.map((ref, index) => {
        const key = ref.chunkId || `${ref.docId}-${index}`;
        // 有 URL 的走链接,本地文件只展示——WebView 里打不开本地路径,
        // 给一个点了没反应的链接比不给更糟。
        const href = ref.url || undefined;
        const Tag = href ? "a" : "span";
        return (
          <Tooltip key={key} title={ref.snippet || ref.path || ref.docTitle}>
            <Tag
              className="hitk-chat-ref"
              {...(href ? { href, target: "_blank", rel: "noreferrer" } : {})}
            >
              {href ? <LinkOutlined /> : <FileTextOutlined />}
              <span className="hitk-chat-ref-title">{ref.docTitle || ref.docId}</span>
              <span className="hitk-chat-ref-score">{ref.score.toFixed(2)}</span>
            </Tag>
          </Tooltip>
        );
      })}
    </div>
  );
}

function ResponseMeta({ body, model, usage }: { body: string; model: string; usage: Usage | null }) {
  return (
    <div className="hitk-chat-response-meta">
      <div className="hitk-chat-response-stats">
        {model && <span className="hitk-chat-response-model">{model}</span>}
        {usage && (
          <>
            <span>输入 {usage.inputTokens.toLocaleString()}</span>
            <span>输出 {usage.outputTokens.toLocaleString()}</span>
            {usage.steps ? <span>{usage.steps} 步</span> : null}
          </>
        )}
      </div>
      <CopyButton body={body} />
    </div>
  );
}

function CopyButton({ body }: { body: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Tooltip title={copied ? "已复制" : "复制"}>
      <Button
        type="text"
        className="hitk-chat-copy-button"
        aria-label="复制回答"
        icon={<CopyOutlined />}
        onClick={() => {
          void navigator.clipboard?.writeText(body).then(
            () => {
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1500);
            },
            () => undefined,
          );
        }}
      />
    </Tooltip>
  );
}
