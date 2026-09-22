import { Button, Checkbox, Divider, Flex, Input, Popover, Progress, Segmented, Tag, Tooltip, Typography } from "antd";
import { CheckOutlined, DatabaseOutlined, EyeOutlined, FileImageOutlined, LoadingOutlined, PaperClipOutlined, PlusOutlined, RightOutlined, SafetyCertificateOutlined, SendOutlined, SettingOutlined, ThunderboltOutlined, UploadOutlined } from "@ant-design/icons";
import { useState } from "react";
import type { Attachment } from "../../../shared/repositories";
import type { CollectionLite } from "../../../shared/types/knowledge";
import type { ModelOptionLite } from "../../../shared/types/settings";
import { optionKey } from "../controllers/useModelControls";

const { Text } = Typography;
const POLICY_OPTIONS = [
  { value: "auto", label: "完全放行", icon: <ThunderboltOutlined /> },
  { value: "manual", label: "手动确认", icon: <SafetyCertificateOutlined /> },
  { value: "read-only", label: "只读", icon: <EyeOutlined /> },
] as const;
const POLICY_LABEL: Record<string, string> = Object.fromEntries(POLICY_OPTIONS.map((option) => [option.value, option.label]));

interface Props {
  input: string; mode: string; sending: boolean; policy: string;
  collections: CollectionLite[]; collectionIds: string[]; attachments: Attachment[];
  modelOptions: ModelOptionLite[]; activeModelKey: string; activeModelLabel: string;
  reasoningEffort: string; reasoningOptions: string[]; contextWindow: number; usedTokens: number; modelReady: boolean; modelLoading: boolean;
  onChangeInput: (value: string) => void; onChangePolicy: (policy: string) => void;
  onChangeCollections: (ids: string[]) => void; onChangeAttachments: (items: Attachment[]) => void;
  onPickFiles: () => void | Promise<void>; onSwitchModel: (provider: string, model: string) => void | Promise<void>;
  onConfigureModels: () => void;
  onChangeReasoningEffort: (effort: string) => void; onSend: () => void | Promise<void>; onStop: () => void | Promise<void>;
}

export function ConversationComposer(props: Props) {
  const sendDisabled = !props.modelReady || (!props.input.trim() && props.attachments.length === 0);
  const percent = props.contextWindow > 0 ? Math.min(100, Math.round((props.usedTokens / props.contextWindow) * 100)) : 0;
  const toggleCollection = (id: string) => props.onChangeCollections(props.collectionIds.includes(id) ? props.collectionIds.filter((value) => value !== id) : [...props.collectionIds, id]);
  return <div className="hitk-chat-composer"><Flex justify="center" className="hitk-chat-composer-track"><div className={`hitk-chat-composer-shell${props.modelReady ? "" : " is-model-unconfigured"}`}>
    {(props.attachments.length > 0 || props.collectionIds.length > 0) && <div className="hitk-chat-composer-context">
      {props.attachments.map((attachment) => <Tag key={attachment.path} closable={!props.sending} onClose={() => props.onChangeAttachments(props.attachments.filter((item) => item.path !== attachment.path))} icon={attachment.kind === "image" ? <FileImageOutlined /> : <PaperClipOutlined />}>{attachment.name}</Tag>)}
      {props.collectionIds.map((id) => { const collection = props.collections.find((item) => item.id === id); return collection ? <Tag key={id} closable={!props.sending} onClose={() => toggleCollection(id)} icon={<DatabaseOutlined />}>{collection.name}</Tag> : null; })}
    </div>}
    <Input.TextArea id="hitk-chat-input" value={props.input} onChange={(event) => props.onChangeInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); if (!sendDisabled) void props.onSend(); } }} placeholder={props.mode === "coding" ? "描述要改的代码或要跑的命令…" : "问点什么，或让我整理资料…"} autoSize={{ minRows: 2, maxRows: 8 }} variant="borderless" disabled={props.sending} />
    <div className="hitk-chat-composer-actions">
      <div className="hitk-chat-composer-actions-left"><AttachmentMenu collections={props.collections} selected={props.collectionIds} disabled={props.sending} onPickFiles={props.onPickFiles} onToggleCollection={toggleCollection} /><PolicyPicker policy={props.policy} disabled={props.sending} onChange={props.onChangePolicy} /></div>
      <div className="hitk-chat-composer-actions-right"><ModelControl {...props} percent={percent} />{props.modelReady && <Tooltip title={props.sending ? "停止生成" : "发送"}><Button type="primary" className={`hitk-chat-send-btn${props.sending ? " hitk-stop-btn" : ""}`} shape="circle" icon={props.sending ? <span className="hitk-stop-btn-glyph" /> : <SendOutlined />} disabled={props.sending ? false : sendDisabled} onClick={() => void (props.sending ? props.onStop() : props.onSend())} /></Tooltip>}</div>
    </div>
  </div></Flex></div>;
}

function AttachmentMenu({ collections, selected, disabled, onPickFiles, onToggleCollection }: { collections: CollectionLite[]; selected: string[]; disabled: boolean; onPickFiles: () => void | Promise<void>; onToggleCollection: (id: string) => void }) { return <Popover trigger="click" placement="topLeft" rootClassName="hitk-chat-attach-popover" content={<div className="hitk-chat-attach-panel"><Button type="text" icon={<UploadOutlined />} onClick={() => void onPickFiles()} block>本地文件</Button><Divider /><Text type="secondary" className="hitk-chat-attach-heading">关联知识库</Text><div className="hitk-chat-collection-list">{collections.length ? collections.map((collection) => <Checkbox key={collection.id} checked={selected.includes(collection.id)} onChange={() => onToggleCollection(collection.id)}>{collection.name}<span className="hitk-chat-collection-count">{collection.docCount}</span></Checkbox>) : <Text type="secondary">暂无知识库</Text>}</div></div>}><Tooltip title="添加文件或关联知识库"><Button type="text" className="hitk-chat-add-trigger" disabled={disabled} icon={<PlusOutlined />} aria-label="添加" /></Tooltip></Popover>; }
function ModelControl(props: Props & { percent: number }) { if (!props.modelReady) return <Button className="hitk-chat-configure-model" type="text" icon={props.modelLoading ? <LoadingOutlined spin /> : <SettingOutlined />} disabled={props.sending || props.modelLoading} onClick={props.onConfigureModels}>{props.modelLoading ? "加载模型" : "配置模型"}</Button>; return <Popover trigger="click" placement="topRight" rootClassName="hitk-chat-model-popover" content={<ModelPanel {...props} />}><Button type="text" className="hitk-chat-model-trigger" title={props.activeModelLabel} disabled={props.sending}><Tooltip title={`上下文 ${props.usedTokens.toLocaleString()} tokens`}><span className="hitk-chat-context-meter">{props.contextWindow > 0 && <Progress type="circle" percent={props.percent} size={14} showInfo={false} strokeWidth={14} />}</span></Tooltip><span className="hitk-chat-model-trigger-name">{props.activeModelLabel}</span>{props.reasoningEffort !== "off" && <span className="hitk-chat-model-trigger-reasoning">{props.reasoningEffort}</span>}<RightOutlined className="hitk-chat-model-trigger-chevron" /></Button></Popover>; }
function ModelPanel(props: Props) {
  const groups = groupByProvider(props.modelOptions);
  return <div className="hitk-chat-model-panel">
    <span className="hitk-chat-model-panel-label">模型</span>
    <div className="hitk-chat-model-list" role="listbox" aria-label="模型">
      {groups.map((group) => <div className="hitk-chat-model-group" key={group.provider}>
        {groups.length > 1 && <span className="hitk-chat-model-provider">{group.label}</span>}
        {group.options.map((option) => {
          const selected = optionKey(option.provider, option.model) === props.activeModelKey;
          return <button type="button" role="option" aria-selected={selected} className={`hitk-chat-model-option${selected ? " is-selected" : ""}`} key={option.model} disabled={props.sending} onClick={() => void props.onSwitchModel(option.provider, option.model)}>
            <span className="hitk-chat-model-option-main"><span>{option.label || option.model}</span>{!option.tools && <span className="hitk-chat-model-capability">无工具 / 知识库 Skill</span>}</span>
            {selected && <CheckOutlined />}
          </button>;
        })}
      </div>)}
    </div>
    {props.reasoningOptions.length > 0 && <><Divider className="hitk-chat-model-panel-divider" /><span className="hitk-chat-model-panel-label">思考等级</span><Segmented className="hitk-chat-reasoning-control" block value={props.reasoningEffort} options={props.reasoningOptions.map((value) => ({ label: value === "off" ? "关闭" : ({ low: "低", medium: "中", high: "高" }[value] ?? value), value }))} onChange={(value) => props.onChangeReasoningEffort(String(value))} /></>}
    {props.contextWindow > 0 && <><Divider className="hitk-chat-model-panel-divider" /><Flex justify="space-between"><span className="hitk-chat-model-panel-label">上下文</span><Text type="secondary">{props.usedTokens.toLocaleString()} / {props.contextWindow.toLocaleString()}</Text></Flex></>}
  </div>;
}
function groupByProvider(options: ModelOptionLite[]) { const groups = new Map<string, { provider: string; label: string; options: ModelOptionLite[] }>(); for (const option of options) { const group = groups.get(option.provider) ?? { provider: option.provider, label: option.providerName, options: [] }; group.options.push(option); groups.set(option.provider, group); } return [...groups.values()]; }
function PolicyPicker({ policy, disabled, onChange }: { policy: string; disabled: boolean; onChange: (policy: string) => void }) {
  const [open, setOpen] = useState(false);
  const choose = (value: string) => {
    onChange(value);
    setOpen(false);
  };
  return <Popover
    trigger="click"
    placement="topLeft"
    arrow={false}
    open={open}
    onOpenChange={setOpen}
    rootClassName="hitk-chat-policy-popover"
    content={<div className="hitk-chat-policy-menu" role="menu" aria-label="执行权限">
      {POLICY_OPTIONS.map((option) => {
        const selected = option.value === policy;
        return <button
          type="button"
          role="menuitemradio"
          aria-checked={selected}
          className={`hitk-chat-policy-option${selected ? " is-selected" : ""}`}
          key={option.value}
          onClick={() => choose(option.value)}
        >
          <span className="hitk-chat-policy-option-icon">{option.icon}</span>
          <span>{option.label}</span>
          {selected && <CheckOutlined className="hitk-chat-policy-option-check" />}
        </button>;
      })}
    </div>}
  ><Button type="text" className="hitk-chat-policy-trigger" disabled={disabled}><SafetyCertificateOutlined /><span>{POLICY_LABEL[policy] ?? "审批策略"}</span></Button></Popover>;
}
