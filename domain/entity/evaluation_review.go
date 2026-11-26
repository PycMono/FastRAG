package entity

import "time"

// EvalCandidate 是题库草稿里的一道候选题，保留出题依据。
// 只有人工标记通过的题目才能进入发布题库。
type EvalCandidate struct {
	SourceType string `json:"source_type,omitempty"` // 来源类型：original_markdown 或知识库切片
	SourceFile string `json:"source_file,omitempty"` // 原始 Markdown 文件名
	FileHash   string `json:"file_hash,omitempty"`   // 原始文件内容哈希
	StartLine  int    `json:"start_line,omitempty"`  // 原始 Markdown 起始行
	EndLine    int    `json:"end_line,omitempty"`    // 原始 Markdown 结束行
	Section    string `json:"section,omitempty"`     // 原文章节
	Region     string `json:"region,omitempty"`      // 原文区域：前/中/后
	Difficulty string `json:"difficulty,omitempty"`  // 出题难度目标
	ID         string `json:"id"`                    // 草稿内题目 ID
	Question   string `json:"question"`              // 生成的问题
	Reference  string `json:"reference"`             // 生成的参考答案
	DocID      string `json:"doc_id"`                // 来源文档 ID
	DocName    string `json:"doc_name"`              // 来源文档名
	ChunkID    string `json:"chunk_id"`              // 来源切片 ID；原始 Markdown 为空
	Source     string `json:"source"`                // 出题依据的原文快照
	SourceHash string `json:"source_hash"`           // 原文快照哈希
	Status     string `json:"status"`                // draft / approved / rejected
	Critical   bool   `json:"critical"`              // 是否关键题
	// GenerationAttempts 只用于后台出题重试；达到上限后跳过该题，避免一道坏题拖停整批。
	GenerationAttempts int `json:"generation_attempts,omitempty"`
}

// EvalDraft 是题库草稿：原文抽样结果、生成状态和人工复核状态。
type EvalDraft struct {
	AutoGenerate    bool            `json:"auto_generate,omitempty"`    // 是否由后台 worker 自动出题
	GenerationState string          `json:"generation_state,omitempty"` // pending / stopped / completed / failed
	GenerationError string          `json:"generation_error,omitempty"` // 后台出题失败原因
	ID              string          `json:"id"`                         // 草稿 ID
	Version         int             `json:"version"`                    // 版本号，用于并发冲突检测
	Tenant          string          `json:"tenant"`                     // 所属租户
	KBNo            string          `json:"kb_no"`                      // 来源知识库
	CreatedAt       time.Time       `json:"created_at"`                 // 创建时间
	Items           []EvalCandidate `json:"items"`                      // 候选题
}

// EvalSamplePolicy 控制从知识库抽样原文的数量。
type EvalSamplePolicy struct {
	PerDoc int `json:"per_doc"` // 每篇文档最多抽多少段；0 表示不单独限制
	Total  int `json:"total"`   // 总段数上限
}

// EvalSourceChunk 是从知识库读取到的一段候选原文。
type EvalSourceChunk struct {
	DocID       string `json:"doc_id"`       // 文档 ID
	DocName     string `json:"doc_name"`     // 文档名
	ChunkID     string `json:"chunk_id"`     // 切片 ID
	Content     string `json:"content"`      // 原文内容
	HeadingPath string `json:"heading_path"` // 章节
	Order       int    `json:"order"`        // 文档内顺序
}

// EvalGeneratedQuestion 是出题模型返回的问题和参考答案。
type EvalGeneratedQuestion struct {
	Question  string `json:"question"`  // 生成的问题
	Reference string `json:"reference"` // 生成的参考答案
}

// EvalPolicy 是每次验收显式提交并保存在报告里的门槛，不代表通用及格线。
type EvalPolicy struct {
	MaxRecallDrop    float64 `json:"max_recall_drop"`    // 允许的覆盖下降
	MaxPrecisionDrop float64 `json:"max_precision_drop"` // 允许的排序下降
	MaxLatencyRatio  float64 `json:"max_latency_ratio"`  // 允许的检索延迟倍率
}

// EvalComparison 是一次基线对比报告。
type EvalComparison struct {
	BaselineID string             `json:"baseline_id"`     // 基线任务 ID
	RunID      string             `json:"run_id"`          // 被比较的任务 ID
	Verdict    string             `json:"verdict"`         // pass / fail / insufficient
	Reasons    []string           `json:"reasons"`         // 判定理由
	Deltas     map[string]float64 `json:"deltas"`          // 各指标相对基线的变化
	Regressed  []string           `json:"regressed_cases"` // 覆盖退步的题目 ID
	Policy     EvalPolicy         `json:"policy"`          // 本次使用的门槛
	CreatedAt  time.Time          `json:"created_at"`      // 报告生成时间
}
