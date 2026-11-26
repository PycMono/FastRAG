package entity

import "time"

// EvalCandidate 保留出题依据。只有人工通过的题目才能发布。
type EvalCandidate struct {
	SourceType string `json:"source_type,omitempty"`
	SourceFile string `json:"source_file,omitempty"`
	FileHash   string `json:"file_hash,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Section    string `json:"section,omitempty"`
	Region     string `json:"region,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
	ID         string `json:"id"`
	Question   string `json:"question"`
	Reference  string `json:"reference"`
	DocID      string `json:"doc_id"`
	DocName    string `json:"doc_name"`
	ChunkID    string `json:"chunk_id"`
	Source     string `json:"source"`
	SourceHash string `json:"source_hash"`
	Status     string `json:"status"`
	Critical   bool   `json:"critical"`
	// GenerationAttempts 只用于后台出题重试；达到上限后跳过该题，避免一道坏题拖停整批。
	GenerationAttempts int `json:"generation_attempts,omitempty"`
}

type EvalDraft struct {
	AutoGenerate    bool            `json:"auto_generate,omitempty"`
	GenerationState string          `json:"generation_state,omitempty"`
	GenerationError string          `json:"generation_error,omitempty"`
	ID              string          `json:"id"`
	Version         int             `json:"version"`
	Tenant          string          `json:"tenant"`
	KBNo            string          `json:"kb_no"`
	CreatedAt       time.Time       `json:"created_at"`
	Items           []EvalCandidate `json:"items"`
}

type EvalSamplePolicy struct {
	PerDoc int `json:"per_doc"`
	Total  int `json:"total"`
}

type EvalSourceChunk struct {
	DocID       string `json:"doc_id"`
	DocName     string `json:"doc_name"`
	ChunkID     string `json:"chunk_id"`
	Content     string `json:"content"`
	HeadingPath string `json:"heading_path"`
	Order       int    `json:"order"`
}

type EvalGeneratedQuestion struct {
	Question  string `json:"question"`
	Reference string `json:"reference"`
}

// EvalPolicy 是每次验收显式提交并保存在报告里的门槛，不代表通用及格线。
type EvalPolicy struct {
	MaxRecallDrop    float64 `json:"max_recall_drop"`
	MaxPrecisionDrop float64 `json:"max_precision_drop"`
	MaxLatencyRatio  float64 `json:"max_latency_ratio"`
}

type EvalComparison struct {
	BaselineID string             `json:"baseline_id"`
	RunID      string             `json:"run_id"`
	Verdict    string             `json:"verdict"`
	Reasons    []string           `json:"reasons"`
	Deltas     map[string]float64 `json:"deltas"`
	Regressed  []string           `json:"regressed_cases"`
	Policy     EvalPolicy         `json:"policy"`
	CreatedAt  time.Time          `json:"created_at"`
}
