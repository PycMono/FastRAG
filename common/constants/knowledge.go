package constants

// 导入形态：调用方给什么，FastRAG 就按什么处理
const (
	FormatMarkdown = "markdown" // 给 markdown 原文，FastRAG 负责切
	FormatText     = "text"     // 给纯文本，走递归分隔符兜底
	FormatChunks   = "chunks"   // 给已切好的切片，只做索引
)

// 检索模式（knowledge_base.search_mode）
const (
	SearchModeTitle           = "title"             // 只查标题
	SearchModeTitleAndContent = "title_and_content" // 标题 + 正文
)

// 知识库类型（knowledge_base.knowledge_type）
const (
	KnowledgeTypeOrdinary = "ordinary"
	KnowledgeTypeQA       = "qa"
)

// 切片默认参数
const (
	DefaultChunkSize  = 800
	DefaultSplitLevel = 2
	DefaultMinChunk   = 50
)

// 检索默认值
const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 100
	MaxIngestChunks    = 5000 // 单文档切片数上限，超过基本可以断定是切错了
)

// 注：向量维度的上限常量（MaxVectorDim）随索引规格一起删掉了。
// 维度现在是 scripts/create-es-index.sh 的 DIM 参数；越界由 ES 自己报错。
