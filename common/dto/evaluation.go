package dto

type EvalCreateDTO struct {
	Dataset        string `json:"dataset" binding:"required,max=64"`
	ExperimentName string `json:"experiment_name" binding:"required,max=120"`
	Hypothesis     string `json:"hypothesis" binding:"max=1000"`
}

type EvalDiagnoseDTO struct {
	Tenant   string `json:"tenant" binding:"required,max=64"`
	KBNo     string `json:"kb_no" binding:"required,max=64"`
	Question string `json:"question" binding:"required,max=1000"`
}

// EvalIDURI 运行任务、草稿均使用服务端生成的安全标识。
type EvalIDURI struct {
	ID string `uri:"id" binding:"required,max=64"`
}

type EvalItemsQuery struct {
	Page int `form:"page,default=1" binding:"gte=1,lte=10000"`
}

type EvalPrepareDTO struct {
	Tenant   string `json:"tenant" binding:"required,max=64"`
	KBNo     string `json:"kb_no" binding:"required,max=64"`
	Count    int    `json:"count" binding:"omitempty,gte=3,lte=1000"`
	PerDoc   int    `json:"per_doc" binding:"omitempty,gte=1,lte=1000"`
	TotalCap int    `json:"total_cap" binding:"omitempty,gte=1,lte=1000"`
}

type EvalGenerateDTO struct {
	CaseID  string `json:"case_id" binding:"required,max=128"`
	Version int    `json:"version" binding:"required,gte=1"`
}

// EvalGenerationDTO 启动/暂停整份草稿的后台出题，只依赖草稿版本。
type EvalGenerationDTO struct {
	Version int `json:"version" binding:"required,gte=1"`
}

// EvalCandidateDTO 只接受人工可编辑字段，来源由服务器恢复。
type EvalCandidateDTO struct {
	ID        string `json:"id" binding:"required,max=128"`
	Question  string `json:"question" binding:"max=1000"`
	Reference string `json:"reference" binding:"max=10000"`
	Status    string `json:"status" binding:"required,oneof=draft approved rejected"`
	Critical  bool   `json:"critical"`
}

type EvalReviewDTO struct {
	Version int                `json:"version" binding:"required,gte=1"`
	Items   []EvalCandidateDTO `json:"items" binding:"required,min=1,max=1000,dive"`
}

type EvalProfileDTO struct {
	Name           string  `json:"name" binding:"required,max=64"`
	Limit          int     `json:"limit" binding:"required,gte=1,lte=100"`
	RetrieveCount  int     `json:"retrieve_count" binding:"required,gte=1,lte=200"`
	DenseWeight    float64 `json:"dense_weight" binding:"gte=0,lte=1"`
	RerankSwitch   bool    `json:"rerank_switch"`
	EmbedModel     string  `json:"embed_model" binding:"max=64"`
	RerankModel    string  `json:"rerank_model" binding:"max=64"`
	MinScore       float64 `json:"min_score" binding:"gte=0"`
	FusionMinScore float64 `json:"fusion_min_score" binding:"gte=0"`
}

type EvalPublishDTO struct {
	Count   int            `json:"count" binding:"gte=0,lte=1000"`
	Version int            `json:"version" binding:"required,gte=1"`
	Profile EvalProfileDTO `json:"profile" binding:"required"`
}

type EvalCompareDTO struct {
	MaxRecallDrop    float64 `json:"max_recall_drop" binding:"gte=0,lte=1"`
	MaxPrecisionDrop float64 `json:"max_precision_drop" binding:"gte=0,lte=1"`
	MaxLatencyRatio  float64 `json:"max_latency_ratio" binding:"required,gte=1,lte=100"`
}
