// 对话视口:滚动容器、空状态与审批弹窗的挂载点。
import type { RefObject, UIEventHandler } from "react";
import { Flex, Typography } from "antd";
import { ArrowRightOutlined } from "@ant-design/icons";
import type { ApprovalRequest, Item, Usage } from "../../../shared/repositories";
import { ConversationMessages } from "./ConversationMessages";
import { ApprovalModal } from "./ApprovalModal";

const { Text } = Typography;

interface Props {
  items: Item[];
  buffers: { text: Record<string, string>; reasoning: Record<string, string> };
  sending: boolean;
  awaitingResponse: boolean;
  running: boolean;
  usage: Usage | null;
  error: string;
  interrupted: boolean;
  approval: ApprovalRequest | null;
  modelLabel: string;
  scrollRef: RefObject<HTMLDivElement>;
  onScroll: UIEventHandler<HTMLDivElement>;
  quickPrompts: string[];
  onQuickPrompt: (prompt: string) => void;
  onResolveApproval: (approve: boolean, remember: boolean) => void | Promise<void>;
}

export function ConversationViewport({
  items,
  buffers,
  sending,
  awaitingResponse,
  running,
  usage,
  error,
  interrupted,
  approval,
  modelLabel,
  scrollRef,
  onScroll,
  quickPrompts,
  onQuickPrompt,
  onResolveApproval,
}: Props) {
  const empty = items.length === 0 && !sending;

  return (
    <div
      className={`hitk-chat-messages ${empty ? "is-empty" : ""}`}
      ref={scrollRef}
      onScroll={onScroll}
      style={{ flex: 1, minWidth: 0, minHeight: 0, overflowY: "auto" }}
    >
      <Flex vertical className="hitk-chat-message-track">
        {empty ? (
          <section className="hitk-chat-empty-state" aria-labelledby="hitk-chat-empty-title">
            <div className="hitk-chat-empty-intro">
              <Typography.Title level={1} id="hitk-chat-empty-title" className="hitk-chat-empty-title">
                你好，我是 HitK
              </Typography.Title>
              <Text type="secondary" className="hitk-chat-empty-copy">
                把资料放进知识库，我就能带着出处回答；切到 Coding 模式还可以读写代码、跑命令。
              </Text>
            </div>
            <div className="hitk-chat-quick-list">
              {quickPrompts.map((prompt) => (
                <button
                  key={prompt}
                  type="button"
                  className="hitk-chat-quick-item ant-btn ant-btn-text"
                  onClick={() => onQuickPrompt(prompt)}
                >
                  <span>{prompt}</span>
                  <ArrowRightOutlined className="hitk-chat-quick-arrow" />
                </button>
              ))}
            </div>
          </section>
        ) : (
          <>
            <ConversationMessages
              items={items}
              buffers={buffers}
              running={running || sending}
              modelLabel={modelLabel}
              usage={usage}
            />
            {awaitingResponse && <ResponsePending />}
            {interrupted && (
              <div className="hitk-agent-process">
                <Text type="secondary" style={{ fontSize: 13 }}>
                  本轮已被中断。
                </Text>
              </div>
            )}
            {error && (
              <div className="hitk-chat-run-error">
                <div className="hitk-chat-run-error-heading">
                  <span>出错了</span>
                </div>
                <div className="hitk-chat-run-error-description">{error}</div>
              </div>
            )}
          </>
        )}
      </Flex>

      {approval && (
        <ApprovalModal
          request={approval}
          resolving={false}
          onResolve={(approve, remember) => void onResolveApproval(approve, remember)}
        />
      )}
    </div>
  );
}

function ResponsePending() {
  return (
    <div className="hitk-chat-response-pending" role="status" aria-live="polite">
      <span className="hitk-chat-response-pending-dots" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <Text type="secondary">正在思考</Text>
    </div>
  );
}
