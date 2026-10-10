package repository

import "context"

// ─── 存储中立的类型 ───────────────────────────────────────────────────────────
//
// 这些类型是「端口自己的语言」：实现方只认它们，不认 entity，
// 所以换 ES → Milvus/Qdrant 时上层一行不用改。

// VectorDoc 一份待写入的切片文档。
type VectorDoc struct {
	ChunkID     string
	Account     string
	KBID        uint64
	DocID       uint64
	Order       int
	BizTag      string
	Title       string
	Content     string
	HeadingPath string
	TitleVec    []float32
	ContentVec  []float32
	CreateTs    int64
}

// VectorFilter 删除条件。零值字段表示「不过滤」。
//
// 全是等值条件：account / kb_id / doc_id。三个都空是「删全库」，
// 实现会拒绝——那几乎一定是调用方漏传了参数。
type VectorFilter struct {
	Account string
	KBID    uint64
	DocID   uint64
}

// VectorSearchReq 检索请求。
type VectorSearchReq struct {
	Account     string
	KBIDs       []uint64
	BizTags     []string
	Query       string
	QueryVec    []float32
	TitleOnly   bool // search_mode = title 时为 true
	DenseWeight float64

	BM25Top       int
	KNNTops       int
	NumCandidates int
}

// VectorHit 一条命中。
type VectorHit struct {
	ChunkID     string
	DocID       uint64
	KBID        uint64
	Order       int
	Title       string
	Content     string
	HeadingPath string
	Score       float64 // 单路原分；融合后的分数由应用层回填
}

// SearchResult 两路原始命中，**未融合**。
//
// 刻意分成两路返回：融合策略（RRF）属于应用层，
// 这样换引擎时权重策略不受影响。
// DenseWeight 让它只发一路时，另一路为 nil。
type SearchResult struct {
	BM25 []VectorHit
	KNN  []VectorHit
}

// ─── 端口 ─────────────────────────────────────────────────────────────────────

// IVectorStore 切片仓储。
//
// 为什么叫 Store 却放在 repository：切片持久化在 ES，
// 它就是切片的仓储，和其他两个仓储地位相同。
type IVectorStore interface {
	// Save 批量写入切片。同一 chunk_id 覆盖。
	Save(ctx context.Context, docs []VectorDoc) error

	// Refresh 强制刷新索引，让刚写入的切片立刻可检索。
	//
	// 两个用途：重导入时让 DeleteExcept 看得见「还没刷出来的旧切片」，
	// 以及调用方要求「导入即可搜」。粒度是整个索引——ES 没有按文档刷新。
	// 它属于「实现细节泄漏到端口」，但 fx 只认接口，放接口上比让装配层
	// 去 import 具体类型省事。
	Refresh(ctx context.Context) error

	// DeleteByQuery 按条件删除**命中的全部**切片，返回删除条数。
	// 用于文档删除 / 整库删除。
	//
	// 调用方**不需要**先调 Refresh：实现负责让这次删除看得见「刚写入、
	// 还没进段」的切片（ES 的 delete_by_query 只作用在已刷新的段上）。
	// 把它留给调用方，漏调一次的后果是接口报 deleted_chunks=0
	// 但切片仍在——静默且难查。
	DeleteByQuery(ctx context.Context, filter VectorFilter) (int64, error)

	// DeleteExcept 删除 filter 命中的切片里 _id **不在** keepIDs 中的那些，返回删除条数。
	//
	// 只用于「写完新切片后清掉旧的」这一步。单独一个方法而不是给
	// DeleteByQuery 加参数：这两者杀伤面差一个数量级——`DeleteExcept` 少传了
	// keepIDs 就等于把整篇文档删空，混在一起早晚出事。
	//
	// filter 必须指定 DocID：没有 doc_id 就没有「这篇文档的旧切片」可言。
	//
	// 失败不影响正确性：旧切片多留一会儿而已，下次重导入会再清一遍。
	DeleteExcept(ctx context.Context, filter VectorFilter, keepIDs []string) (int64, error)

	// Search 发 BM25 / kNN 两路，原样返回，不融合。
	Search(ctx context.Context, req VectorSearchReq) (SearchResult, error)
}
