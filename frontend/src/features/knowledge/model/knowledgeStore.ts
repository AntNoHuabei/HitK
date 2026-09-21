// 知识库状态。
//
// 集合、文档、检索结果都放在这一个 store 里,因为它们互相牵动:删掉集合要清空
// 文档列表,导入完成后统计数字要变,检索结果又只在当前集合范围内有效。分散到
// 多个 store 会让这些联动退化成多处手动同步。
import { create } from "zustand";
import { knowledgeRepository, errorText } from "../../../shared/repositories";
import type {
  CollectionLite,
  DocumentLite,
  ImportOutcomeLite,
  KnowledgeStatsLite,
  SearchHitLite,
} from "../../../shared/types/knowledge";

interface KnowledgeState {
  collections: CollectionLite[];
  activeCollectionId: string;
  documents: DocumentLite[];
  stats: KnowledgeStatsLite | null;
  hits: SearchHitLite[];
  /** 已提交检索的关键词,用于在结果区回显范围。 */
  query: string;
  loading: boolean;
  loadingDocuments: boolean;
  searching: boolean;
  importing: boolean;
  error: string;

  load: () => Promise<void>;
  selectCollection: (id: string) => Promise<void>;
  createCollection: (name: string, description: string) => Promise<CollectionLite>;
  updateCollection: (id: string, name: string, description: string) => Promise<void>;
  deleteCollection: (id: string) => Promise<void>;
  removeDocument: (id: string) => Promise<void>;
  importFiles: (paths: string[]) => Promise<string>;
  importDirectory: (dir: string) => Promise<string>;
  importUrl: (url: string) => Promise<string>;
  importText: (title: string, body: string) => Promise<string>;
  search: (query: string) => Promise<void>;
  clearSearch: () => void;
  reindex: () => Promise<void>;
}

export const useKnowledgeStore = create<KnowledgeState>((set, get) => {
  /** 导入成功后刷新文档列表与统计,让界面立刻反映新内容。 */
  const refreshAfterImport = async () => {
    const { activeCollectionId } = get();
    if (activeCollectionId) {
      set({ documents: await knowledgeRepository.documents(activeCollectionId) });
    }
    set({ stats: await knowledgeRepository.stats() });
  };

  /** 所有导入入口的共用收尾:跑完导入 → 刷新 → 返回一句可展示的结果。 */
  const runImport = async (task: () => Promise<ImportOutcomeLite>): Promise<string> => {
    set({ importing: true, error: "" });
    try {
      const outcome = await task();
      await refreshAfterImport();
      set({ importing: false });
      return describeImport(outcome);
    } catch (err) {
      set({ importing: false, error: errorText(err) });
      throw err;
    }
  };

  return {
    collections: [],
    activeCollectionId: "",
    documents: [],
    stats: null,
    hits: [],
    query: "",
    loading: false,
    loadingDocuments: false,
    searching: false,
    importing: false,
    error: "",

    load: async () => {
      set({ loading: true, error: "" });
      try {
        const [collections, stats] = await Promise.all([
          knowledgeRepository.collections(),
          knowledgeRepository.stats(),
        ]);
        set({ collections, stats, loading: false });
        // 当前集合消失(被删或首次进入)时退回第一个,避免右侧面板空转。
        const { activeCollectionId } = get();
        if (activeCollectionId && !collections.some((c) => c.id === activeCollectionId)) {
          await get().selectCollection(collections[0]?.id ?? "");
        } else if (!activeCollectionId && collections.length > 0) {
          await get().selectCollection(collections[0].id);
        }
      } catch (err) {
        set({ loading: false, error: errorText(err) });
      }
    },

    selectCollection: async (id) => {
      set({ activeCollectionId: id, documents: [], hits: [], query: "" });
      if (!id) return;
      set({ loadingDocuments: true });
      try {
        set({ documents: await knowledgeRepository.documents(id), loadingDocuments: false });
      } catch (err) {
        set({ loadingDocuments: false, error: errorText(err) });
      }
    },

    createCollection: async (name, description) => {
      const created = await knowledgeRepository.createCollection(name, description);
      await get().load();
      await get().selectCollection(created.id);
      return created;
    },

    updateCollection: async (id, name, description) => {
      await knowledgeRepository.updateCollection(id, name, description);
      await get().load();
    },

    deleteCollection: async (id) => {
      await knowledgeRepository.deleteCollection(id);
      if (get().activeCollectionId === id) set({ activeCollectionId: "", documents: [] });
      await get().load();
    },

    removeDocument: async (id) => {
      await knowledgeRepository.deleteDocument(id);
      await refreshAfterImport();
    },

    importFiles: (paths) =>
      runImport(() => knowledgeRepository.addFiles(get().activeCollectionId, paths)),

    importDirectory: (dir) =>
      runImport(() => knowledgeRepository.addDirectory(get().activeCollectionId, dir, true)),

    importUrl: (url) => runImport(() => knowledgeRepository.addUrl(get().activeCollectionId, url)),

    importText: (title, body) =>
      runImport(() => knowledgeRepository.addText(get().activeCollectionId, title, body)),

    search: async (query) => {
      const trimmed = query.trim();
      if (!trimmed) {
        set({ hits: [], query: "" });
        return;
      }
      set({ searching: true, query: trimmed, error: "" });
      try {
        const hits = await knowledgeRepository.search(trimmed, get().activeCollectionId, 12);
        set({ hits, searching: false });
      } catch (err) {
        set({ searching: false, hits: [], error: errorText(err) });
      }
    },

    clearSearch: () => set({ hits: [], query: "" }),

    reindex: async () => {
      await knowledgeRepository.reindex();
      set({ stats: await knowledgeRepository.stats() });
    },
  };
});

/** describeImport 把导入计数说成人话。 */
function describeImport(outcome: ImportOutcomeLite): string {
  const parts = [`新增 ${outcome.added} 份`];
  if (outcome.skipped > 0) parts.push(`跳过 ${outcome.skipped} 份`);
  if (outcome.failed.length > 0) parts.push(`失败 ${outcome.failed.length} 份`);
  return parts.join(",");
}
