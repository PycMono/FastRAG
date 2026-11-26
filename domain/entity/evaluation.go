package entity

import "time"

// EvalCase 是一道固定的人工评测标准。
// 问题、参考答案和参考证据在发布题库时冻结；检索结果不写在这里，另存于 EvalRow。
type EvalCase struct {
	Difficulty string   `json:"difficulty,omitempty"` // 出题难度目标，仅用于人工筛选，不参与评分
	ID         string   `json:"id"`                   // 题库内唯一题目 ID，重跑时保持不变
	Question   string   `json:"question"`             // 发送给检索服务的问题
	Tenant     string   `json:"tenant"`               // 评测所属租户
	KBNos      []string `json:"kb_nos"`               // 本次检索范围，一个或多个知识库编号
	Category   string   `json:"category"`             // 题目分类，例如 reviewed-source、boundary
	Split      string   `json:"split"`                // 数据拆分；当前只允许 dev
	Answerable bool     `json:"answerable"`           // 是否存在参考答案；无答案题只评相关性
	Critical   bool     `json:"critical"`             // 关键题；退步时直接判定验收失败
	Reference  string   `json:"reference"`            // 人工核对的参考答案，用于覆盖率和排序判断
	// ReferenceSources 保存参考答案对应的原文出处，发布时用于核验来源未变化。
	ReferenceSources []EvalSource `json:"reference_sources"`
	// ReferenceContextIDs 是参考证据的 chunk ID；有值时排序和覆盖走确定性 ID 指标。
	ReferenceContextIDs []string `json:"reference_context_ids"`
	NoAnswerReason      string   `json:"no_answer_reason,omitempty"`    // 无答案题的原因说明
	ForbiddenChunkIDs   []string `json:"forbidden_chunk_ids,omitempty"` // 检索命中即视为越界的片段
	ForbiddenDocIDs     []string `json:"forbidden_doc_ids,omitempty"`   // 检索命中即视为越界的文档
	ExpectedRetrieval   string   `json:"expected_retrieval,omitempty"`  // 边界预期；当前只支持 empty
}

// EvalSource 是一条参考证据的出处。
// 原始 Markdown 题库保存文件、行号和证据快照；从知识库抽样时保存 chunk 与文档 ID。
type EvalSource struct {
	SourceType   string `json:"source_type,omitempty"`   // original_markdown 或知识库切片
	SourceFile   string `json:"source_file,omitempty"`   // 原始 Markdown 文件名
	FileHash     string `json:"file_hash,omitempty"`     // 原始文件内容哈希
	StartLine    int    `json:"start_line,omitempty"`    // 原始 Markdown 起始行
	EndLine      int    `json:"end_line,omitempty"`      // 原始 Markdown 结束行
	Evidence     string `json:"evidence,omitempty"`      // 证据原文快照
	EvidenceHash string `json:"evidence_hash,omitempty"` // 证据快照哈希
	DocID        string `json:"doc_id"`                  // 文档 ID
	HeadingPath  string `json:"heading_path"`            // 证据所在章节
}

// EvalProfile 是一次评测固定的检索配置；发布题库时写入，之后每次重跑复用。
type EvalProfile struct {
	Name           string  `json:"name"`             // 配置名，例如 hybrid、production
	Limit          int     `json:"limit"`            // 最终返回给评分的条数
	RetrieveCount  int     `json:"retrieve_count"`   // 送入精排的候选数
	DenseWeight    float64 `json:"dense_weight"`     // 向量路权重；0 只走 BM25，1 只走 kNN
	RerankSwitch   bool    `json:"rerank_switch"`    // 是否调用精排模型
	EmbedModel     string  `json:"embed_model"`      // 本次使用的向量模型名
	RerankModel    string  `json:"rerank_model"`     // 本次使用的精排模型名
	MinScore       float64 `json:"min_score"`        // 最终分阈值，0 表示不过滤
	FusionMinScore float64 `json:"fusion_min_score"` // 精排前的融合分阈值，0 表示不过滤
}

// EvalManifest 记录题库版本、语料快照和检索设置，用于保证回归可比性。
type EvalManifest struct {
	Reviewed          bool           `json:"reviewed"`           // 是否经过人工复核
	DatasetVersion    string         `json:"dataset_version"`    // 题库版本名，通常等于发布名
	AnnotationVersion string         `json:"annotation_version"` // 标注版本
	CorpusSnapshot    string         `json:"corpus_snapshot"`    // 语料快照类型，例如 live-source-sample
	CorpusHash        string         `json:"corpus_hash"`        // 题库快照哈希
	RetrievalSettings map[string]any `json:"retrieval_settings"` // 服务级检索设置和来源哈希
}

// EvalDataset 是一个不可变题库版本：manifest、逐题标准和检索配置一起冻结。
type EvalDataset struct {
	Name     string        `json:"name"`     // 题库名，发布后唯一
	Manifest EvalManifest  `json:"manifest"` // 版本和来源快照
	Cases    []EvalCase    `json:"cases"`    // 逐题标准
	Profiles []EvalProfile `json:"profiles"` // 一组或多组检索配置
}

// EvalDatasetInfo 是题库列表的展示信息，不携带完整题目快照。
type EvalDatasetInfo struct {
	Tenant         string   `json:"tenant"`          // 题库所属租户
	KBNos          []string `json:"kb_nos"`          // 覆盖的知识库
	Name           string   `json:"name"`            // 题库名
	Version        string   `json:"version"`         // 题库版本
	Cases          int      `json:"cases"`           // 题目数
	Profiles       int      `json:"profiles"`        // 检索配置数
	ProfileSummary string   `json:"profile_summary"` // 检索配置摘要
	SearchRequests int      `json:"search_requests"` // 预计检索次数 = 题目数 × 配置数
}

// EvalMetric 是单个指标在一次评分中的结果。
type EvalMetric struct {
	ErrorDetails map[string]any `json:"error_details,omitempty"` // 安全的错误分类，例如 category、stage
	Name         string         `json:"name"`                    // 指标名，例如 ContextPrecision
	Status       string         `json:"status"`                  // ok / error / timeout / not_applicable / cancelled
	Value        *float64       `json:"value"`                   // 有效分数；失败时为 null
	Error        string         `json:"error,omitempty"`         // 面向用户的错误说明
	ComputedBy   string         `json:"computed_by"`             // ragas / id_based / policy / transport
	LatencyMS    float64        `json:"latency_ms"`              // 该指标耗时
}

// EvalScoreInput 是一次评分请求：问题、参考答案、检索片段及其 ID。
type EvalScoreInput struct {
	RequestID           string   `json:"request_id"`            // 评分请求 ID，用于关联结果
	DatasetVersion      string   `json:"dataset_version"`       // 题库版本
	AnnotationVersion   string   `json:"annotation_version"`    // 标注版本
	CaseID              string   `json:"case_id"`               // 题目 ID
	UserInput           string   `json:"user_input"`            // 问题
	Reference           string   `json:"reference"`             // 参考答案
	RetrievedContexts   []string `json:"retrieved_contexts"`    // 检索返回的片段正文
	RetrievedContextIDs []string `json:"retrieved_context_ids"` // 检索返回的片段 ID
	ReferenceContextIDs []string `json:"reference_context_ids"` // 参考证据的片段 ID
	Metrics             []string `json:"metrics"`               // 本次需要计算的指标
}

// EvalScoreOutput 是一次评分请求的结果，包含逐指标状态和批次元数据。
type EvalScoreOutput struct {
	RequestID     string         `json:"request_id"`     // 与输入一致的请求 ID
	OverallStatus string         `json:"overall_status"` // succeeded / partial
	Metrics       []EvalMetric   `json:"metrics"`        // 逐指标结果
	Metadata      map[string]any `json:"metadata"`       // 裁判模型版本、配置哈希等
}

// EvalHit 是检索返回的一条片段。
type EvalHit struct {
	ChunkID     string  `json:"chunk_id"`     // 片段 ID
	DocID       string  `json:"doc_id"`       // 文档 ID
	Content     string  `json:"content"`      // 片段正文
	Title       string  `json:"title"`        // 片段标题
	HeadingPath string  `json:"heading_path"` // 片段章节
	Score       float64 `json:"score"`        // 检索或精排分数
}

// EvalRetrieval 是一次检索的完整结果。
type EvalRetrieval struct {
	Items       []EvalHit      `json:"items"`       // 按最终排序返回的片段
	Meta        map[string]any `json:"meta"`        // 检索元数据，例如是否降级
	Diagnostics map[string]any `json:"diagnostics"` // 检索诊断信息
}

// EvalRow 是一道题在一次配置下的完整执行记录。
type EvalRow struct {
	CaseID             string           `json:"case_id"`                   // 题目 ID
	Question           string           `json:"question"`                  // 问题
	Reference          string           `json:"reference"`                 // 参考答案
	Profile            string           `json:"profile"`                   // 检索配置名
	Category           string           `json:"category"`                  // 题目分类
	Split              string           `json:"split"`                     // 数据拆分
	RetrievalStatus    string           `json:"retrieval_status"`          // ok / empty / error / timeout
	RetrievalError     string           `json:"retrieval_error,omitempty"` // 检索失败原因
	RetrievalLatencyMS float64          `json:"retrieval_latency_ms"`      // 检索耗时
	ScoringLatencyMS   float64          `json:"scoring_latency_ms"`        // 评分耗时
	Retrieval          *EvalRetrieval   `json:"retrieval,omitempty"`       // 检索结果
	Input              *EvalScoreInput  `json:"input,omitempty"`           // 送入评分的输入
	Score              *EvalScoreOutput `json:"score,omitempty"`           // 评分结果
	BoundaryViolation  bool             `json:"boundary_violation"`        // 是否越过禁止片段或预期空结果
}

// EvalSummary 是一个 profile/metric 组合的汇总。
type EvalSummary struct {
	Mean          *float64 `json:"mean"`           // 有效评分的均值；没有有效分时为 null
	OK            int      `json:"ok"`             // 有效评分数量
	NotApplicable int      `json:"not_applicable"` // 不适用的数量
	Errors        int      `json:"errors"`         // 失败数量；不计入均值
	Coverage      float64  `json:"coverage"`       // 有效评分 / (有效 + 失败)
}

// EvalRun 是一次评测任务的完整快照，包含题库、逐题结果和汇总。
type EvalRun struct {
	ID                string                 `json:"id"`                 // 任务 ID
	ExperimentName    string                 `json:"experiment_name"`    // 实验名
	Hypothesis        string                 `json:"hypothesis"`         // 本次变更假设
	Purpose           string                 `json:"purpose"`            // regression / diagnostic
	Status            string                 `json:"status"`             // queued/running/succeeded/partial/failed/cancelled/timed_out/interrupted
	Error             string                 `json:"error,omitempty"`    // 任务级错误
	CreatedAt         time.Time              `json:"created_at"`         // 创建时间
	FinishedAt        *time.Time             `json:"finished_at"`        // 结束时间；运行中为 null
	Dataset           EvalDataset            `json:"dataset"`            // 任务当时的题库快照
	ConfigHash        string                 `json:"config_hash"`        // 检索配置哈希
	Metadata          map[string]any         `json:"metadata"`           // 数据集哈希、重评来源等
	Expected          int                    `json:"expected"`           // 预计执行题次
	Done              int                    `json:"done"`               // 已完成题次
	RetrievalFailures int                    `json:"retrieval_failures"` // 检索失败数
	BoundaryFailures  int                    `json:"boundary_failures"`  // 边界违规数
	RerankDegraded    int                    `json:"rerank_degraded"`    // 精排降级次数
	ScoringFailures   int                    `json:"scoring_failures"`   // 评分失败指标数
	Rows              []EvalRow              `json:"rows,omitempty"`     // 逐题结果
	Summary           map[string]EvalSummary `json:"summary"`            // profile/metric 汇总
}
