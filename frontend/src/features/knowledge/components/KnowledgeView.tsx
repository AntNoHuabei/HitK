// 知识库视图。
//
// 左集合 + 右内容(文档 / 检索)。视图只负责把 store 的动作接到组件上,联动逻辑
// 都在 knowledgeStore 里,这样「导入后自动刷新」「删集合退回第一个」这类规则只有
// 一处实现。
import { useEffect, useState } from "react";
import { App as AntApp, Button, Segmented, Tooltip, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { errorText } from "../../../shared/repositories";
import { useKnowledgeStore } from "../model/knowledgeStore";
import { CollectionPanel } from "./CollectionPanel";
import { DocumentPanel } from "./DocumentPanel";
import { SearchPanel } from "./SearchPanel";

const { Text } = Typography;

type Tab = "documents" | "search";

export function KnowledgeView() {
  const { message } = AntApp.useApp();
  const [tab, setTab] = useState<Tab>("documents");

  const collections = useKnowledgeStore((state) => state.collections);
  const activeId = useKnowledgeStore((state) => state.activeCollectionId);
  const documents = useKnowledgeStore((state) => state.documents);
  const stats = useKnowledgeStore((state) => state.stats);
  const hits = useKnowledgeStore((state) => state.hits);
  const query = useKnowledgeStore((state) => state.query);
  const loading = useKnowledgeStore((state) => state.loading);
  const loadingDocuments = useKnowledgeStore((state) => state.loadingDocuments);
  const searching = useKnowledgeStore((state) => state.searching);
  const importing = useKnowledgeStore((state) => state.importing);
  const error = useKnowledgeStore((state) => state.error);

  useEffect(() => {
    void useKnowledgeStore.getState().load();
  }, []);

  useEffect(() => {
    if (error) void message.error(error);
  }, [error, message]);

  const active = collections.find((collection) => collection.id === activeId) ?? null;

  return (
    <div className="hitk-kb">
      <CollectionPanel
        collections={collections}
        activeId={activeId}
        onSelect={(id) => void useKnowledgeStore.getState().selectCollection(id)}
        onCreate={async (name, description) => {
          await useKnowledgeStore.getState().createCollection(name, description);
          void message.success("已创建");
        }}
        onUpdate={useKnowledgeStore.getState().updateCollection}
        onDelete={useKnowledgeStore.getState().deleteCollection}
      />

      <div className="hitk-kb-main">
        <div className="hitk-kb-head">
          <div className="hitk-kb-head-text">
            <h2 className="hitk-kb-title">{active?.name ?? "知识库"}</h2>
            <div className="hitk-kb-subtitle">
              {active
                ? active.description || storageLine(active.docCount, active.chunkCount, active.sizeBytes)
                : "新建一个知识库,把资料放进去,agent 就能检索并引用它们"}
            </div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <Segmented
              size="small"
              value={tab}
              onChange={(value) => setTab(value as Tab)}
              options={[
                { label: "文档", value: "documents" },
                { label: "检索", value: "search" },
              ]}
            />
            <Tooltip
              title={
                stats
                  ? `全局索引：${stats.collections} 库 / ${stats.documents} 文档 / ${stats.chunks} 分块`
                  : "重建索引"
              }
            >
              <Button
                type="text"
                icon={<ReloadOutlined />}
                aria-label="重建索引"
                onClick={() => {
                  void useKnowledgeStore
                    .getState()
                    .reindex()
                    .then(() => message.success("索引已重建"))
                    .catch((err) => message.error(errorText(err)));
                }}
              />
            </Tooltip>
          </div>
        </div>

        {!active ? (
          <div className="hitk-kb-body">
            <div className="hitk-kb-empty">
              <Text type="secondary">左侧还没有选中的知识库</Text>
            </div>
          </div>
        ) : tab === "documents" ? (
          <DocumentPanel
            documents={documents}
            loading={loadingDocuments || loading}
            importing={importing}
            disabled={importing}
            onImportFiles={async (paths) => {
              const summary = await useKnowledgeStore.getState().importFiles(paths);
              void message.success(summary);
            }}
            onImportDirectory={async (dir) => {
              const summary = await useKnowledgeStore.getState().importDirectory(dir);
              void message.success(summary);
            }}
            onImportUrl={async (url) => {
              const summary = await useKnowledgeStore.getState().importUrl(url);
              void message.success(summary);
            }}
            onImportText={async (title, body) => {
              const summary = await useKnowledgeStore.getState().importText(title, body);
              void message.success(summary);
            }}
            onDelete={useKnowledgeStore.getState().removeDocument}
          />
        ) : (
          <SearchPanel
            hits={hits}
            query={query}
            searching={searching}
            disabled={false}
            onSearch={(next) => void useKnowledgeStore.getState().search(next)}
          />
        )}
      </div>
    </div>
  );
}

/** storageLine 在没有简介时用体量信息补位。 */
function storageLine(docCount: number, chunkCount: number, sizeBytes: number): string {
  if (!docCount) return "还没有文档";
  const parts = [`${docCount} 份文档`, `${chunkCount} 个分块`];
  if (sizeBytes > 0) {
    parts.push(sizeBytes < 1024 * 1024 ? `${(sizeBytes / 1024).toFixed(1)} KB` : `${(sizeBytes / 1024 / 1024).toFixed(1)} MB`);
  }
  return parts.join(" · ");
}
