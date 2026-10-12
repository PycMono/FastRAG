package constants

// 切片列表的默认值与上限，服务于 GET /api/v1/docs/chunks。
//
// 不复用 MaxSearchLimit（100）：切片比文档小得多，而这段的用途就是「看全貌」。
// 只做「封顶 + 总数」，不做深翻页。
//
// 单开一文件而不是并进 knowledge.go：那组知识库常量与切片列表无关，
// 且 knowledge.go 此刻正被另一条改动线占着，另起一文件避免抢同一块工作树。
const (
	DefaultChunkListLimit = 200
	MaxChunkListLimit     = 500
)
