// 知识库后端调用(集合、文档、检索)。
import { KB } from "./bindings";
import { list, text } from "./helpers";
import type { ImportResult } from "./bindings";
import type {
  CollectionLite,
  DocumentLite,
  ImportOutcomeLite,
  KnowledgeStatsLite,
  SearchHitLite,
} from "../types/knowledge";

function toCollection(input: {
  id: string;
  name: string;
  description?: string;
  icon?: string;
  color?: string;
  createdAt: number;
  updatedAt: number;
  docCount: number;
  chunkCount: number;
  sizeBytes: number;
  embedderId?: string;
  readOnly?: boolean;
}): CollectionLite {
  return {
    id: input.id,
    name: input.name,
    description: text(input.description),
    icon: text(input.icon),
    color: text(input.color),
    createdAt: input.createdAt,
    updatedAt: input.updatedAt,
    docCount: input.docCount,
    chunkCount: input.chunkCount,
    sizeBytes: input.sizeBytes,
    embedderId: text(input.embedderId),
    readOnly: input.readOnly ?? false,
  };
}

function toDocument(input: {
  id: string;
  collectionId: string;
  title: string;
  source: string;
  kind: string;
  mime?: string;
  sizeBytes: number;
  createdAt: number;
  updatedAt: number;
  chunkCount: number;
  status: string;
  error?: string;
}): DocumentLite {
  return {
    id: input.id,
    collectionId: input.collectionId,
    title: input.title,
    source: input.source,
    kind: input.kind,
    mime: text(input.mime),
    sizeBytes: input.sizeBytes,
    createdAt: input.createdAt,
    updatedAt: input.updatedAt,
    chunkCount: input.chunkCount,
    status: input.status,
    error: text(input.error),
  };
}

function toOutcome(result: ImportResult): ImportOutcomeLite {
  return {
    added: result.added,
    skipped: result.skipped,
    failed: list(result.failed),
    message: result.message,
  };
}

export const knowledgeRepository = {
  collections: async (): Promise<CollectionLite[]> =>
    list(await KB.Collections()).map(toCollection),

  createCollection: async (name: string, description = ""): Promise<CollectionLite> =>
    toCollection(await KB.CreateCollection(name, description)),

  updateCollection: async (id: string, name: string, description = ""): Promise<CollectionLite> =>
    toCollection(await KB.UpdateCollection(id, name, description)),

  deleteCollection: (id: string): Promise<void> => KB.DeleteCollection(id),

  documents: async (collectionId: string): Promise<DocumentLite[]> =>
    list(await KB.Documents(collectionId)).map(toDocument),

  deleteDocument: (id: string): Promise<void> => KB.DeleteDocument(id),

  stats: async (): Promise<KnowledgeStatsLite> => {
    const raw = await KB.Stats();
    return {
      collections: raw.collections,
      documents: raw.documents,
      chunks: raw.chunks,
      sizeBytes: raw.sizeBytes,
      indexed: raw.indexed,
    };
  },

  embedderId: (): Promise<string> => KB.EmbedderID(),

  addFiles: async (collectionId: string, paths: string[]): Promise<ImportOutcomeLite> =>
    toOutcome(await KB.AddFiles(collectionId, paths)),

  addDirectory: async (
    collectionId: string,
    dir: string,
    recursive = true,
  ): Promise<ImportOutcomeLite> => toOutcome(await KB.AddDirectory(collectionId, dir, recursive)),

  addUrl: async (collectionId: string, url: string): Promise<ImportOutcomeLite> =>
    toOutcome(await KB.AddURL(collectionId, url)),

  addText: async (
    collectionId: string,
    title: string,
    body: string,
  ): Promise<ImportOutcomeLite> => toOutcome(await KB.AddText(collectionId, title, body)),

  search: async (query: string, collectionId = "", limit = 12): Promise<SearchHitLite[]> => {
    const hits = list(await KB.Search(query, collectionId, limit));
    return hits.map((hit) => ({
      chunkId: hit.chunkId,
      documentId: hit.documentId,
      docTitle: hit.docTitle,
      collection: hit.collection,
      source: hit.source,
      heading: text(hit.heading),
      snippet: hit.snippet,
      score: hit.score,
      lexical: hit.lexical,
      vector: hit.vector,
    }));
  },

  documentContent: (documentId: string): Promise<string> => KB.DocumentContent(documentId),

  reindex: (): Promise<void> => KB.Reindex(),

  extensions: async (): Promise<string[]> => list(await KB.SupportedExtensions()),
};
