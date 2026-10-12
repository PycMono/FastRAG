package dto

import "github.com/PycMono/FastRAG/common/constants"

// DocListQuery 列某库下文档的查询参数（GET /api/v1/docs）。
//
// 本仓第一次出现「GET + query 参数」：tag 用 form，控制器走 ShouldBindQuery。
// 分页直接内嵌现成的 PageQuery——默认值与上限规整它已经带了，别再自己算一遍。
type DocListQuery struct {
	Account string `form:"account" binding:"required,max=64"`
	KBNo    string `form:"kb_no" binding:"required,max=64"`
	PageQuery
}

// DocDetailQuery 单篇详情的查询参数（GET /api/v1/docs/detail）。
//
// 按 doc_id 而不是 kb_no + doc_name：页面上的每个入口（检索结果、文档列表）
// 手里都是 doc_id，它是稳定主键、改名不影响；name 是可变的展示身份。
type DocDetailQuery struct {
	Account string `form:"account" binding:"required,max=64"`
	DocID   uint64 `form:"doc_id" binding:"required"`
}

// ChunkListQuery 某文档切片的查询参数（GET /api/v1/docs/chunks）。
//
// 不复用 PageQuery：它的 page_size 上限写死 100，这里要 200 起步、封顶 500。
//
// limit 的 binding 里**故意不写 lte**：传 9999 应当被**钳到** 500，而不是像
// 超限的 page_size 那样直接报参数错误——两者的调用方不同，切片列表是给人翻的，
// 别因为多传了一位就整页报错。钳制在 Normalize 里做。
type ChunkListQuery struct {
	Account string `form:"account" binding:"required,max=64"`
	DocID   uint64 `form:"doc_id" binding:"required"`
	Limit   int    `form:"limit,default=200" binding:"omitempty,gte=1"`
	Offset  int    `form:"offset,default=0"`
}

// Normalize 规整 limit / offset：负数 offset 归零、超限 limit 钳到上限。
func (q *ChunkListQuery) Normalize() {
	if q.Limit < 1 {
		q.Limit = constants.DefaultChunkListLimit
	}
	if q.Limit > constants.MaxChunkListLimit {
		q.Limit = constants.MaxChunkListLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
}
