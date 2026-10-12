package dto

import domainservice "github.com/PycMono/FastRAG/domain/service"

// DocParseDTO 文档解析导入请求：给一个 URL，服务端解析成 markdown 后入库。
//
// 与 DocIngestDTO 的关系：它是「URL + 解析参数」，解析出 markdown 之后走的是同一条
// 下游（切分 → 向量化 → 写 ES）。它**没有** format 字段——解析产物恒是 markdown，
// 形态由文件类型决定，不由调用方声明。
//
// 它也**没有**选后端的字段：用哪家由 config.parser.default 决定，想换就改配置重启
// （用户 2026-10-12 裁定）。
type DocParseDTO struct {
	Account string `json:"account" binding:"required,max=64"`
	KBNo    string `json:"kb_no" binding:"required,max=64"`

	// DocName 可省；为空时服务端取 URL 末段，再推不出就退化成稳定的哈希名。
	DocName string `json:"doc_name" binding:"max=512"`

	// URL 必须是解析后端能公网下载到的地址（腾讯要求下载时限 3 秒）。
	// FastRAG 自己不抓它，只是转交给后端。
	URL string `json:"url" binding:"required,url,max=2048"`

	// Model 语义同 DocIngestDTO.Model：这批数据用哪家算向量。
	Model string `json:"model" binding:"omitempty,max=64"`

	SplitOptions *SplitOptionsDTO `json:"split_options"`
	Refresh      bool             `json:"refresh"`
}

// ChunkOptions 解析本次导入的切片参数，规则与 DocIngestDTO.ChunkOptions 完全一致。
func (in *DocParseDTO) ChunkOptions() (domainservice.ChunkOptions, error) {
	var opts domainservice.ChunkOptions
	if o := in.SplitOptions; o != nil {
		opts = domainservice.ChunkOptions{
			ChunkSize:  o.ChunkSize,
			SplitLevel: o.SplitLevel,
			MinChunk:   o.MinChunk,
		}
	}
	return opts.Normalize()
}
