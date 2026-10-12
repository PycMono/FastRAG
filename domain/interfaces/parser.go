package interfaces

import "context"

// ParseReq 一次解析请求。
//
// 只带 URL，不带文件字节——上传不在本方案范围内（设计文档 §1）。
// Filename 由 URL 末段推得，用于判类型：有的后端（腾讯）要求显式给 FileType，
// 有的（Docling）不要求，但拿它来判「这个格式我支不支持」都够用。
type ParseReq struct {
	URL      string
	Filename string
}

// ParseResult 解析产物：**只有 markdown**。
//
// 不产出 text/chunks 是有意的：splitter.Split 已经能处理 markdown（带标题链，
// 切片质量最好），而 Docling 与腾讯都能出 markdown。只出纯文本的后端由它自己的
// 适配器转成 markdown，端口对外只承诺 markdown，下游因此零改动。
type ParseResult struct {
	Markdown string
}

// IParser 一个解析后端。
type IParser interface {
	// Name 配置里的 key，日志用。
	Name() string

	// Formats 该后端支持的扩展名（小写、不含点）。请求的扩展名不在这个集合里，
	// 应用层直接返回参数错误，不往下走。空集合表示「不声明、一律放行」。
	Formats() []string

	// Parse 把 URL 上的文档解析成 markdown。
	Parse(ctx context.Context, req ParseReq) (ParseResult, error)
}

// IParserRegistry 按名字取解析后端。
//
// 与 IEmbeddingRegistry 同构：名字来自请求体，取不到时返回参数错误——
// 绝不能悄悄回落到默认那家。
type IParserRegistry interface {
	Get(name string) (IParser, error) // name 为空 → 配置里的 default
}
