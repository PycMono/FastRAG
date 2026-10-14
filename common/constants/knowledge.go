package constants

// 导入形态：调用方给什么，FastRAG 就按什么处理
const (
	FormatMarkdown = "markdown" // 给 markdown 原文，FastRAG 负责切
	FormatText     = "text"     // 给纯文本，走递归分隔符兜底
	FormatChunks   = "chunks"   // 给已切好的切片，只做索引
)

// 切片默认参数
const (
	DefaultChunkSize  = 800
	DefaultSplitLevel = 2
	DefaultMinChunk   = 50
)

// 单篇原文的字节上限：列是 MEDIUMTEXT（16MB），留一半余量。
//
// 超限**拒绝导入并报错**，不截断——静默截断会让详情页显示的正文与
// 检索命中的切片来自两个不同的世界，而这种不一致完全看不出来。
const MaxContentBytes = 8 << 20 // 8 MiB

// 检索默认值
const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 100
	MaxIngestChunks    = 5000 // 单文档切片数上限，超过基本可以断定是切错了
)
