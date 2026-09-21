// 知识库领域的 UI 类型。
export interface CollectionLite {
  id: string;
  name: string;
  description: string;
  icon: string;
  color: string;
  createdAt: number;
  updatedAt: number;
  docCount: number;
  chunkCount: number;
  sizeBytes: number;
  embedderId: string;
  readOnly: boolean;
}

export interface DocumentLite {
  id: string;
  collectionId: string;
  title: string;
  source: string;
  kind: string;
  mime: string;
  sizeBytes: number;
  createdAt: number;
  updatedAt: number;
  chunkCount: number;
  status: string;
  error: string;
}

export interface KnowledgeStatsLite {
  collections: number;
  documents: number;
  chunks: number;
  sizeBytes: number;
  indexed: boolean;
}

export interface SearchHitLite {
  chunkId: string;
  documentId: string;
  docTitle: string;
  collection: string;
  source: string;
  heading: string;
  snippet: string;
  score: number;
  lexical: number;
  vector: number;
}

/** 一次导入的结果。 */
export interface ImportOutcomeLite {
  added: number;
  skipped: number;
  failed: string[];
  message: string;
}
