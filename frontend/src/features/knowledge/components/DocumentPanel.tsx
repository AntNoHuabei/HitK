// 文档列表与导入工具条。
import { useState } from "react";
import { App as AntApp, Button, Empty, Flex, Input, Modal, Spin, Tag, Typography } from "antd";
import {
  DeleteOutlined,
  ExportOutlined,
  FileAddOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { appRepository, errorText } from "../../../shared/repositories";
import { docAccent, humanBytes, relativeTime, shortSource } from "../../../shared/lib/format";
import type { DocumentLite } from "../../../shared/types/knowledge";

const { Text } = Typography;

interface Props {
  documents: DocumentLite[];
  loading: boolean;
  importing: boolean;
  disabled: boolean;
  onImportFiles: (paths: string[]) => Promise<void>;
  onImportDirectory: (dir: string) => Promise<void>;
  onImportUrl: (url: string) => Promise<void>;
  onImportText: (title: string, body: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

export function DocumentPanel({
  documents,
  loading,
  importing,
  disabled,
  onImportFiles,
  onImportDirectory,
  onImportUrl,
  onImportText,
  onDelete,
}: Props) {
  const { message } = AntApp.useApp();
  const [urlsOpen, setUrlsOpen] = useState(false);
  const [textOpen, setTextOpen] = useState(false);
  const [filter, setFilter] = useState("");

  const run = async (task: () => Promise<void>, done: string) => {
    try {
      await task();
      void message.success(done);
    } catch (err) {
      void message.error(errorText(err));
    }
  };

  const pickFiles = async () => {
    const paths = await appRepository.pickFiles("选择要导入的文档");
    if (paths.length === 0) return;
    await run(() => onImportFiles(paths), "导入完成");
  };

  const pickDirectory = async () => {
    const dir = await appRepository.pickDirectory("选择要导入的目录");
    if (!dir) return;
    await run(() => onImportDirectory(dir), "导入完成");
  };

  const visible = filter.trim()
    ? documents.filter((doc) =>
        `${doc.title} ${doc.source}`.toLowerCase().includes(filter.trim().toLowerCase()),
      )
    : documents;

  return (
    <>
      <div className="hitk-kb-toolbar">
        <Button
          type="primary"
          icon={<FileAddOutlined />}
          loading={importing}
          disabled={disabled}
          onClick={() => void pickFiles()}
        >
          导入文件
        </Button>
        <Button icon={<FolderOpenOutlined />} disabled={disabled} onClick={() => void pickDirectory()}>
          导入目录
        </Button>
        <Button icon={<LinkOutlined />} disabled={disabled} onClick={() => setUrlsOpen(true)}>
          从网址
        </Button>
        <Button icon={<FileTextOutlined />} disabled={disabled} onClick={() => setTextOpen(true)}>
          粘贴文本
        </Button>
        <div style={{ flex: 1 }} />
        <Input
          allowClear
          size="small"
          style={{ width: 190 }}
          placeholder="筛选文档"
          prefix={<SearchOutlined />}
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        />
      </div>

      <div className="hitk-kb-body">
        {loading ? (
          <Flex justify="center" style={{ padding: 40 }}>
            <Spin />
          </Flex>
        ) : visible.length === 0 ? (
          <div className="hitk-kb-empty">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                documents.length === 0
                  ? "还没有文档，导入文件 / 目录 / 网址，或直接粘贴一段文本"
                  : "没有匹配的文档"
              }
            />
          </div>
        ) : (
          visible.map((doc) => {
            const accent = docAccent(doc);
            return (
              <div key={doc.id} className="hitk-kb-doc">
                <span className="hitk-kb-doc-icon" style={{ color: accent.color }}>
                  <span style={{ fontSize: 10, fontWeight: 700 }}>{accent.label}</span>
                </span>
                <span className="hitk-kb-doc-body">
                  <span className="hitk-kb-doc-title" title={doc.title}>
                    {doc.title}
                  </span>
                  <span className="hitk-kb-doc-meta">
                    <span>{shortSource(doc.source)}</span>
                    {doc.chunkCount > 0 && <span>{doc.chunkCount} 分块</span>}
                    {doc.sizeBytes > 0 && <span>{humanBytes(doc.sizeBytes)}</span>}
                    <span>{relativeTime(doc.updatedAt || doc.createdAt)}</span>
                    {doc.status !== "ready" && (
                      <Tag
                        color={doc.status === "failed" ? "red" : "gold"}
                        style={{ fontSize: 11, lineHeight: "16px", margin: 0 }}
                      >
                        {doc.status === "failed" ? "解析失败" : "处理中"}
                      </Tag>
                    )}
                  </span>
                </span>
                <span className="hitk-kb-doc-actions">
                  <Button
                    type="text"
                    size="small"
                    aria-label="在文件管理器中显示"
                    icon={<ExportOutlined />}
                    disabled={doc.kind === "url" || doc.kind === "text"}
                    onClick={() => void appRepository.reveal(doc.source).catch(() => undefined)}
                  />
                  <Button
                    type="text"
                    size="small"
                    danger
                    aria-label="删除文档"
                    icon={<DeleteOutlined />}
                    onClick={() =>
                      void run(async () => {
                        await onDelete(doc.id);
                      }, "已删除")
                    }
                  />
                </span>
              </div>
            );
          })
        )}
      </div>

      <UrlEditor
        open={urlsOpen}
        onClose={() => setUrlsOpen(false)}
        onSubmit={async (url) => {
          await onImportUrl(url);
          setUrlsOpen(false);
        }}
      />
      <TextEditor
        open={textOpen}
        onClose={() => setTextOpen(false)}
        onSubmit={async (title, body) => {
          await onImportText(title, body);
          setTextOpen(false);
        }}
      />
    </>
  );
}

function UrlEditor({
  open,
  onClose,
  onSubmit,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (url: string) => Promise<void>;
}) {
  const { message } = AntApp.useApp();
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const urls = value
      .split(/\s+/)
      .map((item) => item.trim())
      .filter(Boolean);
    if (urls.length === 0) return;
    setBusy(true);
    try {
      for (const url of urls) {
        await onSubmit(url);
      }
      setValue("");
    } catch (err) {
      void message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title="从网址导入"
      okText="导入"
      cancelText="取消"
      confirmLoading={busy}
      onCancel={onClose}
      onOk={() => void submit()}
    >
      <Input.TextArea
        rows={4}
        placeholder={"每行一个网址\nhttps://example.com/doc"}
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
      <Text type="secondary" style={{ fontSize: 12 }}>
        需要启用联网工具；正文会被抓取、解析后入库。
      </Text>
    </Modal>
  );
}

function TextEditor({
  open,
  onClose,
  onSubmit,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (title: string, body: string) => Promise<void>;
}) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!body.trim()) return;
    setBusy(true);
    try {
      await onSubmit(title.trim() || "粘贴的文本", body);
      setTitle("");
      setBody("");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title="粘贴文本入库"
      okText="入库"
      cancelText="取消"
      confirmLoading={busy}
      onCancel={onClose}
      onOk={() => void submit()}
    >
      <Flex vertical gap={10}>
        <Input
          placeholder="标题（可选）"
          maxLength={80}
          value={title}
          onChange={(event) => setTitle(event.target.value)}
        />
        <Input.TextArea
          rows={8}
          placeholder="把要入库的正文粘贴到这里"
          value={body}
          onChange={(event) => setBody(event.target.value)}
        />
      </Flex>
    </Modal>
  );
}
