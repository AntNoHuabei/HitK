import { Button, Divider, Flex, Popover, Progress, Segmented, Tooltip, Typography } from "antd";
import {
  CheckOutlined,
  LoadingOutlined,
  RightOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import type { ModelOptionLite } from "../../../shared/types/settings";
import { optionKey } from "../../../shared/lib/modelKey";

const { Text } = Typography;

export interface ModelControlProps {
  modelOptions: ModelOptionLite[];
  activeModelKey: string;
  activeModelLabel: string;
  reasoningEffort: string;
  reasoningOptions: string[];
  contextWindow: number;
  usedTokens: number;
  modelReady: boolean;
  modelLoading: boolean;
  disabled: boolean;
  onChangeModel: (provider: string, model: string) => void | Promise<void>;
  onConfigureModels: () => void;
  onChangeReasoningEffort: (effort: string) => void;
  placement?: "topRight" | "topLeft";
}

export function ModelControl(props: ModelControlProps) {
  const percent =
    props.contextWindow > 0
      ? Math.min(100, Math.round((props.usedTokens / props.contextWindow) * 100))
      : 0;

  if (!props.modelReady) {
    return (
      <Button
        className="hitk-chat-configure-model"
        type="text"
        icon={props.modelLoading ? <LoadingOutlined spin /> : <SettingOutlined />}
        disabled={props.disabled || props.modelLoading}
        onClick={props.onConfigureModels}
      >
        {props.modelLoading ? "加载模型" : "配置模型"}
      </Button>
    );
  }

  return (
    <Popover
      trigger="click"
      placement={props.placement ?? "topRight"}
      rootClassName="hitk-chat-model-popover"
      content={<ModelPanel {...props} />}
    >
      <Button
        type="text"
        className="hitk-chat-model-trigger"
        title={props.activeModelLabel}
        disabled={props.disabled}
      >
        <Tooltip title={`上下文 ${props.usedTokens.toLocaleString()} tokens`}>
          <span className="hitk-chat-context-meter">
            {props.contextWindow > 0 && (
              <Progress
                type="circle"
                percent={percent}
                size={14}
                showInfo={false}
                strokeWidth={14}
              />
            )}
          </span>
        </Tooltip>
        <span className="hitk-chat-model-trigger-name">{props.activeModelLabel}</span>
        {props.reasoningEffort !== "off" && (
          <span className="hitk-chat-model-trigger-reasoning">
            {props.reasoningEffort}
          </span>
        )}
        <RightOutlined className="hitk-chat-model-trigger-chevron" />
      </Button>
    </Popover>
  );
}

function ModelPanel(props: ModelControlProps) {
  const groups = groupByProvider(props.modelOptions);
  return (
    <div className="hitk-chat-model-panel">
      <span className="hitk-chat-model-panel-label">模型</span>
      <div className="hitk-chat-model-list" role="listbox" aria-label="模型">
        {groups.map((group) => (
          <div className="hitk-chat-model-group" key={group.provider}>
            {groups.length > 1 && (
              <span className="hitk-chat-model-provider">{group.label}</span>
            )}
            {group.options.map((option) => {
              const selected =
                optionKey(option.provider, option.model) === props.activeModelKey;
              return (
                <button
                  type="button"
                  role="option"
                  aria-selected={selected}
                  className={`hitk-chat-model-option${selected ? " is-selected" : ""}`}
                  key={option.model}
                  disabled={props.disabled}
                  onClick={() => void props.onChangeModel(option.provider, option.model)}
                >
                  <span className="hitk-chat-model-option-main">
                    <span>{option.label || option.model}</span>
                    {!option.tools && (
                      <span className="hitk-chat-model-capability">
                        无工具 / 知识库 Skill
                      </span>
                    )}
                  </span>
                  {selected && <CheckOutlined />}
                </button>
              );
            })}
          </div>
        ))}
      </div>
      {props.reasoningOptions.length > 0 && (
        <>
          <Divider className="hitk-chat-model-panel-divider" />
          <span className="hitk-chat-model-panel-label">思考等级</span>
          <Segmented
            className="hitk-chat-reasoning-control"
            block
            value={props.reasoningEffort}
            options={props.reasoningOptions.map((value) => ({
              label: reasoningLabel(value),
              value,
            }))}
            onChange={(value) => props.onChangeReasoningEffort(String(value))}
          />
        </>
      )}
      {props.contextWindow > 0 && (
        <>
          <Divider className="hitk-chat-model-panel-divider" />
          <Flex justify="space-between">
            <span className="hitk-chat-model-panel-label">上下文</span>
            <Text type="secondary">
              {props.usedTokens.toLocaleString()} / {props.contextWindow.toLocaleString()}
            </Text>
          </Flex>
        </>
      )}
    </div>
  );
}

function groupByProvider(options: ModelOptionLite[]) {
  const groups = new Map<
    string,
    { provider: string; label: string; options: ModelOptionLite[] }
  >();
  for (const option of options) {
    const group =
      groups.get(option.provider) ??
      { provider: option.provider, label: option.providerName, options: [] };
    group.options.push(option);
    groups.set(option.provider, group);
  }
  return [...groups.values()];
}

function reasoningLabel(value: string): string {
  return { off: "关闭", low: "低", medium: "中", high: "高" }[value] ?? value;
}
