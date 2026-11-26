package entity

import "time"

// EvalCase 是固定的人工评测标准，检索结果另存于 EvalRow。
type EvalCase struct {
	Difficulty          string       `json:"difficulty,omitempty"`
	ID                  string       `json:"id"`
	Question            string       `json:"question"`
	Tenant              string       `json:"tenant"`
	KBNos               []string     `json:"kb_nos"`
	Category            string       `json:"category"`
	Split               string       `json:"split"`
	Answerable          bool         `json:"answerable"`
	Critical            bool         `json:"critical"`
	Reference           string       `json:"reference"`
	ReferenceSources    []EvalSource `json:"reference_sources"`
	ReferenceContextIDs []string     `json:"reference_context_ids"`
	NoAnswerReason      string       `json:"no_answer_reason,omitempty"`
	ForbiddenChunkIDs   []string     `json:"forbidden_chunk_ids,omitempty"`
	ForbiddenDocIDs     []string     `json:"forbidden_doc_ids,omitempty"`
	ExpectedRetrieval   string       `json:"expected_retrieval,omitempty"`
}

type EvalSource struct {
	SourceType   string `json:"source_type,omitempty"`
	SourceFile   string `json:"source_file,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	StartLine    int    `json:"start_line,omitempty"`
	EndLine      int    `json:"end_line,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
	EvidenceHash string `json:"evidence_hash,omitempty"`
	DocID        string `json:"doc_id"`
	HeadingPath  string `json:"heading_path"`
}

type EvalProfile struct {
	Name           string  `json:"name"`
	Limit          int     `json:"limit"`
	RetrieveCount  int     `json:"retrieve_count"`
	DenseWeight    float64 `json:"dense_weight"`
	RerankSwitch   bool    `json:"rerank_switch"`
	EmbedModel     string  `json:"embed_model"`
	RerankModel    string  `json:"rerank_model"`
	MinScore       float64 `json:"min_score"`
	FusionMinScore float64 `json:"fusion_min_score"`
}

type EvalManifest struct {
	Reviewed          bool           `json:"reviewed"`
	DatasetVersion    string         `json:"dataset_version"`
	AnnotationVersion string         `json:"annotation_version"`
	CorpusSnapshot    string         `json:"corpus_snapshot"`
	CorpusHash        string         `json:"corpus_hash"`
	RetrievalSettings map[string]any `json:"retrieval_settings"`
}

type EvalDataset struct {
	Name     string        `json:"name"`
	Manifest EvalManifest  `json:"manifest"`
	Cases    []EvalCase    `json:"cases"`
	Profiles []EvalProfile `json:"profiles"`
}

type EvalDatasetInfo struct {
	Tenant         string   `json:"tenant"`
	KBNos          []string `json:"kb_nos"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Cases          int      `json:"cases"`
	Profiles       int      `json:"profiles"`
	ProfileSummary string   `json:"profile_summary"`
	SearchRequests int      `json:"search_requests"`
}

type EvalMetric struct {
	ErrorDetails map[string]any `json:"error_details,omitempty"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	Value        *float64       `json:"value"`
	Error        string         `json:"error,omitempty"`
	ComputedBy   string         `json:"computed_by"`
	LatencyMS    float64        `json:"latency_ms"`
}

type EvalScoreInput struct {
	RequestID           string   `json:"request_id"`
	DatasetVersion      string   `json:"dataset_version"`
	AnnotationVersion   string   `json:"annotation_version"`
	CaseID              string   `json:"case_id"`
	UserInput           string   `json:"user_input"`
	Reference           string   `json:"reference"`
	RetrievedContexts   []string `json:"retrieved_contexts"`
	RetrievedContextIDs []string `json:"retrieved_context_ids"`
	ReferenceContextIDs []string `json:"reference_context_ids"`
	Metrics             []string `json:"metrics"`
}

type EvalScoreOutput struct {
	RequestID     string         `json:"request_id"`
	OverallStatus string         `json:"overall_status"`
	Metrics       []EvalMetric   `json:"metrics"`
	Metadata      map[string]any `json:"metadata"`
}

type EvalHit struct {
	ChunkID     string  `json:"chunk_id"`
	DocID       string  `json:"doc_id"`
	Content     string  `json:"content"`
	Title       string  `json:"title"`
	HeadingPath string  `json:"heading_path"`
	Score       float64 `json:"score"`
}

type EvalRetrieval struct {
	Items       []EvalHit      `json:"items"`
	Meta        map[string]any `json:"meta"`
	Diagnostics map[string]any `json:"diagnostics"`
}

type EvalRow struct {
	CaseID             string           `json:"case_id"`
	Question           string           `json:"question"`
	Reference          string           `json:"reference"`
	Profile            string           `json:"profile"`
	Category           string           `json:"category"`
	Split              string           `json:"split"`
	RetrievalStatus    string           `json:"retrieval_status"`
	RetrievalError     string           `json:"retrieval_error,omitempty"`
	RetrievalLatencyMS float64          `json:"retrieval_latency_ms"`
	ScoringLatencyMS   float64          `json:"scoring_latency_ms"`
	Retrieval          *EvalRetrieval   `json:"retrieval,omitempty"`
	Input              *EvalScoreInput  `json:"input,omitempty"`
	Score              *EvalScoreOutput `json:"score,omitempty"`
	BoundaryViolation  bool             `json:"boundary_violation"`
}

type EvalSummary struct {
	Mean          *float64 `json:"mean"`
	OK            int      `json:"ok"`
	NotApplicable int      `json:"not_applicable"`
	Errors        int      `json:"errors"`
	Coverage      float64  `json:"coverage"`
}

type EvalRun struct {
	ID                string                 `json:"id"`
	ExperimentName    string                 `json:"experiment_name"`
	Hypothesis        string                 `json:"hypothesis"`
	Purpose           string                 `json:"purpose"`
	Status            string                 `json:"status"`
	Error             string                 `json:"error,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	FinishedAt        *time.Time             `json:"finished_at"`
	Dataset           EvalDataset            `json:"dataset"`
	ConfigHash        string                 `json:"config_hash"`
	Metadata          map[string]any         `json:"metadata"`
	Expected          int                    `json:"expected"`
	Done              int                    `json:"done"`
	RetrievalFailures int                    `json:"retrieval_failures"`
	BoundaryFailures  int                    `json:"boundary_failures"`
	RerankDegraded    int                    `json:"rerank_degraded"`
	Rows              []EvalRow              `json:"rows,omitempty"`
	Summary           map[string]EvalSummary `json:"summary"`
}
