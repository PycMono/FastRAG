package vo

import "time"

type EvalCreatedVO struct {
	RunID string `json:"run_id"`
}

type EvalDatasetInfoVO struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Tenant         string   `json:"tenant"`
	KBNos          []string `json:"kb_nos"`
	Cases          int      `json:"cases"`
	Profiles       int      `json:"profiles"`
	ProfileSummary string   `json:"profile_summary"`
	SearchRequests int      `json:"search_requests"`
}

type EvalSummaryVO struct {
	Mean          *float64 `json:"mean"`
	OK            int      `json:"ok"`
	Errors        int      `json:"errors"`
	NotApplicable int      `json:"not_applicable"`
	Coverage      float64  `json:"coverage"`
}

// EvalRunVO 不暴露持久化快照、参考答案全集或内部配置。
type EvalRunVO struct {
	Diagnostics       map[string]EvalDiagnosticsVO `json:"diagnostics"`
	FailureReasons    []string                     `json:"failure_reasons"`
	ScoringFailures   int                          `json:"scoring_failures"`
	ID                string                       `json:"id"`
	Dataset           string                       `json:"dataset"`
	ProfileSummary    string                       `json:"profile_summary"`
	ExperimentName    string                       `json:"experiment_name"`
	Purpose           string                       `json:"purpose"`
	Status            string                       `json:"status"`
	Error             string                       `json:"error,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	FinishedAt        *time.Time                   `json:"finished_at"`
	Expected          int                          `json:"expected"`
	Done              int                          `json:"done"`
	RetrievalFailures int                          `json:"retrieval_failures"`
	BoundaryFailures  int                          `json:"boundary_failures"`
	RerankDegraded    int                          `json:"rerank_degraded"`
	Summary           map[string]EvalSummaryVO     `json:"summary"`
}

// EvalDiagnosticsVO 检索工程指标与有标注时的二值排序指标，按 profile 汇总。
type EvalDiagnosticsVO struct {
	Requests          int      `json:"requests"`
	Successful        int      `json:"successful"`
	Empty             int      `json:"empty"`
	FailureRate       float64  `json:"failure_rate"`
	EmptyRate         float64  `json:"empty_rate"`
	P50MS             *float64 `json:"p50_ms"`
	P95MS             *float64 `json:"p95_ms"`
	ScoringP95MS      *float64 `json:"scoring_p95_ms"`
	AverageResults    float64  `json:"average_results"`
	AverageCharacters float64  `json:"average_characters"`
	DuplicateRate     float64  `json:"duplicate_rate"`
	ScoreCoverage     float64  `json:"score_coverage"`
	LabelledQueries   int      `json:"labelled_queries"`
	RecallAt5         *float64 `json:"recall_at_5"`
	PrecisionAt5      *float64 `json:"precision_at_5"`
	MRRAt5            *float64 `json:"mrr_at_5"`
	SuccessAt5        *float64 `json:"success_at_5"`
	BinaryNDCGAt5     *float64 `json:"binary_ndcg_at_5"`
}

type EvalMetricVO struct {
	ErrorDetails map[string]any `json:"error_details,omitempty"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	Value        *float64       `json:"value"`
	Error        string         `json:"error,omitempty"`
	ComputedBy   string         `json:"computed_by"`
}

type EvalHitVO struct {
	ChunkID string `json:"chunk_id"`
	DocID   string `json:"doc_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type EvalRetrievalVO struct {
	Items []EvalHitVO `json:"items"`
}

type EvalScoreVO struct {
	Metrics []EvalMetricVO `json:"metrics"`
}

type EvalRowVO struct {
	CaseID             string           `json:"case_id"`
	Question           string           `json:"question"`
	Reference          string           `json:"reference"`
	Profile            string           `json:"profile"`
	RetrievalStatus    string           `json:"retrieval_status"`
	RetrievalError     string           `json:"retrieval_error,omitempty"`
	RetrievalLatencyMS float64          `json:"retrieval_latency_ms"`
	ScoringLatencyMS   float64          `json:"scoring_latency_ms"`
	BoundaryViolation  bool             `json:"boundary_violation"`
	Retrieval          *EvalRetrievalVO `json:"retrieval,omitempty"`
	Score              *EvalScoreVO     `json:"score,omitempty"`
}

type EvalItemsVO struct {
	Items []EvalRowVO `json:"items"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
	Limit int         `json:"limit"`
}

type EvalCandidateVO struct {
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
}

type EvalDraftVO struct {
	AutoGenerate    bool              `json:"auto_generate,omitempty"`
	GenerationState string            `json:"generation_state,omitempty"`
	GenerationError string            `json:"generation_error,omitempty"`
	ID              string            `json:"id"`
	Version         int               `json:"version"`
	Tenant          string            `json:"tenant"`
	KBNo            string            `json:"kb_no"`
	CreatedAt       time.Time         `json:"created_at"`
	Items           []EvalCandidateVO `json:"items"`
}

type EvalPublishedVO struct {
	Dataset string `json:"dataset"`
}

type EvalComparisonVO struct {
	BaselineID string             `json:"baseline_id"`
	RunID      string             `json:"run_id"`
	Verdict    string             `json:"verdict"`
	Reasons    []string           `json:"reasons"`
	Deltas     map[string]float64 `json:"deltas"`
	Regressed  []string           `json:"regressed_cases"`
	CreatedAt  time.Time          `json:"created_at"`
}
