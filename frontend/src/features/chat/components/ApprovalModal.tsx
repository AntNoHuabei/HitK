// 审批弹窗。
//
// agent 掌握写操作的门控,所以这个弹窗只在 agent 决定需要征询时出现;工具自身
// 提出的展示需求会复用同一次回答,不会二次弹窗。
import { Alert, Button, Checkbox, Input, Modal, Space, Tag, Typography } from "antd";
import { useState } from "react";
import { WarningOutlined } from "@ant-design/icons";
import type { ApprovalRequest } from "../../../shared/repositories";

interface Props {
  request: ApprovalRequest;
  onResolve: (approve: boolean, remember: boolean) => void;
  resolving: boolean;
}

export function ApprovalModal({ request, onResolve, resolving }: Props) {
  const [remember, setRemember] = useState(false);

  return (
    <Modal
      open
      title="需要确认"
      width={620}
      maskClosable={false}
      keyboard={false}
      closable={false}
      footer={
        <Space>
          <Button danger onClick={() => onResolve(false, remember)} loading={resolving}>
            拒绝
          </Button>
          <Button type="primary" onClick={() => onResolve(true, remember)} loading={resolving}>
            允许
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={10} style={{ width: "100%" }}>
        <Space size={8}>
          <WarningOutlined style={{ color: "var(--ant-color-warning, #d48806)" }} />
          <Typography.Text strong>{request.title || request.toolName}</Typography.Text>
          <Tag>{request.toolName}</Tag>
        </Space>

        {request.detail && (
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            {request.detail}
          </Typography.Paragraph>
        )}

        {request.command && (
          <Input.TextArea value={request.command} readOnly autoSize={{ minRows: 2, maxRows: 8 }} />
        )}

        {request.diff && (
          <Input.TextArea value={request.diff} readOnly autoSize={{ minRows: 4, maxRows: 16 }} />
        )}

        <Alert
          type="info"
          showIcon
          message="允许后本轮内同名的写操作不再询问。这项豁免只对当前这一轮生效。"
        />
        <Checkbox checked={remember} onChange={(event) => setRemember(event.target.checked)}>
          这一轮内不再询问
        </Checkbox>
      </Space>
    </Modal>
  );
}
