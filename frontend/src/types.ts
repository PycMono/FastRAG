export interface Workspace {
  tenant: string
  kb_no: string
}
export interface Doc {
  doc_id: string
  doc_name: string
  title: string
  chunk_count: number
  has_content: boolean
  create_ts: number
  update_ts: number
}
export interface DocDetail extends Doc {
  content: string
  source_url: string
  content_hash: string
}
export interface Page<T> {
  total: number
  list: T[] | null
  page: number
  page_size: number
  pages: number
}
export interface Chunk {
  chunk_id: string
  order: number
  title: string
  content: string
  heading_path: string
}
export interface ChunkPage {
  total: number
  offset: number
  limit: number
  items: Chunk[] | null
}
export interface SearchHit extends Chunk {
  doc_id: string
  doc_name: string
  kb_no: string
  kb_name: string
  score: number
  diagnostics?: HitDiagnostics
}
export interface IngestResult {
  doc_id: string
  doc_name: string
  chunk_count: number
  created: boolean
}
export interface HitDiagnostics {
  bm25_rank: number
  bm25_score?: number
  knn_rank: number
  knn_score?: number
  fusion_score: number
  rerank_score?: number
}
export interface SearchMeta {
  score_type: 'rrf' | 'rerank' | 'mixed'
  rerank_requested: boolean
  rerank_applied: boolean
  degraded: boolean
  threshold_skipped: boolean
  warnings: string[]
}
export interface SearchDiagnostics {
  bm25_top: number
  knn_top: number
  num_candidates: number
  rerank_budget: number
  dense_weight: number
  rank_constant: number
  counts: Record<string, number>
  timings_ms: Record<string, number>
}
export interface SearchResult {
  items: SearchHit[] | null
  meta?: SearchMeta
  diagnostics?: SearchDiagnostics
}
