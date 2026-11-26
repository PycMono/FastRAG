package entity

// 以下结构体是评测模块的 GORM 表模型。
// 评测领域对象是嵌套快照（Dataset/Cases/Rows），无法直接映射成单行；
// 这里只保留表字段和 JSON 文本，领域转换留在仓储层。

// EvalDatasetRecord 对应 eval_dataset：一个不可变题库版本一行。
type EvalDatasetRecord struct {
	Name           string `gorm:"column:name;primaryKey"` // 题库版本名
	Version        string `gorm:"column:version"`         // manifest.dataset_version
	Tenant         string `gorm:"column:tenant"`          // 所属租户
	KBNos          string `gorm:"column:kb_nos"`          // 覆盖的知识库编号 JSON
	CasesCount     int    `gorm:"column:cases_count"`     // 题目数
	ProfilesCount  int    `gorm:"column:profiles_count"`  // 检索配置数
	ProfileSummary string `gorm:"column:profile_summary"` // 检索配置摘要
	SearchRequests int    `gorm:"column:search_requests"` // 预计检索次数
	Manifest       string `gorm:"column:manifest"`        // manifest JSON
	Profiles       string `gorm:"column:profiles"`        // 检索配置 JSON
	Cases          string `gorm:"column:cases"`           // 逐题标准 JSON
	Retired        bool   `gorm:"column:retired"`         // 关联文档删除后置 1
	CreatedMS      int64  `gorm:"column:created_ms"`      // 创建时间，UTC 毫秒
}

func (EvalDatasetRecord) TableName() string { return "eval_dataset" }

// EvalRunRecord 对应 eval_run：任务元数据、题库快照和汇总。
type EvalRunRecord struct {
	ID                string `gorm:"column:id;primaryKey"`      // 任务 ID
	DatasetName       string `gorm:"column:dataset_name"`       // 题库名
	ExperimentName    string `gorm:"column:experiment_name"`    // 实验名
	Hypothesis        string `gorm:"column:hypothesis"`         // 本次变更假设
	Purpose           string `gorm:"column:purpose"`            // regression / diagnostic
	Status            string `gorm:"column:status"`             // 任务状态
	Error             string `gorm:"column:error"`              // 任务级错误
	ConfigHash        string `gorm:"column:config_hash"`        // 检索配置哈希
	Metadata          string `gorm:"column:metadata"`           // 任务元数据 JSON
	DatasetSnapshot   string `gorm:"column:dataset_snapshot"`   // 题库快照 JSON
	Profiles          string `gorm:"column:profiles"`           // 检索配置 JSON，列表展示用
	Expected          int    `gorm:"column:expected"`           // 预计执行题次
	Done              int    `gorm:"column:done"`               // 已完成题次
	RetrievalFailures int    `gorm:"column:retrieval_failures"` // 检索失败数
	BoundaryFailures  int    `gorm:"column:boundary_failures"`  // 边界违规数
	RerankDegraded    int    `gorm:"column:rerank_degraded"`    // 精排降级次数
	ScoringFailures   int    `gorm:"column:scoring_failures"`   // 评分失败指标数
	Summary           string `gorm:"column:summary"`            // 指标汇总 JSON
	CreatedMS         int64  `gorm:"column:created_ms"`         // 创建时间，UTC 毫秒
	FinishedMS        *int64 `gorm:"column:finished_ms"`        // 结束时间，UTC 毫秒
}

func (EvalRunRecord) TableName() string { return "eval_run" }

// EvalRunRowRecord 对应 eval_run_row：一题一配置一行，payload 是完整 EvalRow JSON。
type EvalRunRowRecord struct {
	ID      uint64 `gorm:"column:id;primaryKey;autoIncrement"` // 自增 ID，保留追加顺序
	RunID   string `gorm:"column:run_id"`                      // 任务 ID
	CaseID  string `gorm:"column:case_id"`                     // 题目 ID
	Profile string `gorm:"column:profile"`                     // 检索配置名
	Payload string `gorm:"column:payload"`                     // EvalRow JSON
}

func (EvalRunRowRecord) TableName() string { return "eval_run_row" }

// EvalDraftRecord 对应 eval_draft：题库草稿与后台出题状态。
type EvalDraftRecord struct {
	ID              string `gorm:"column:id;primaryKey"`    // 草稿 ID
	Tenant          string `gorm:"column:tenant"`           // 所属租户
	KBNo            string `gorm:"column:kb_no"`            // 来源知识库
	Version         int    `gorm:"column:version"`          // 版本号
	AutoGenerate    bool   `gorm:"column:auto_generate"`    // 是否后台自动出题
	GenerationState string `gorm:"column:generation_state"` // pending / stopped / completed / failed
	GenerationError string `gorm:"column:generation_error"` // 后台出题失败原因
	Items           string `gorm:"column:items"`            // 候选题 JSON
	CreatedMS       int64  `gorm:"column:created_ms"`       // 创建时间，UTC 毫秒
}

func (EvalDraftRecord) TableName() string { return "eval_draft" }

// EvalBaselineRecord 对应 eval_baseline：一个题库版本一条基线。
type EvalBaselineRecord struct {
	DatasetName string `gorm:"column:dataset_name;primaryKey"` // 题库名
	RunID       string `gorm:"column:run_id"`                  // 基线任务 ID
	CreatedMS   int64  `gorm:"column:created_ms"`              // 设置时间，UTC 毫秒
}

func (EvalBaselineRecord) TableName() string { return "eval_baseline" }

// EvalComparisonRecord 对应 eval_comparison：一次基线对比报告。
type EvalComparisonRecord struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement"` // 自增 ID
	RunID      string `gorm:"column:run_id"`                      // 被比较的任务 ID
	BaselineID string `gorm:"column:baseline_id"`                 // 基线任务 ID
	Verdict    string `gorm:"column:verdict"`                     // pass / fail / insufficient
	Payload    string `gorm:"column:payload"`                     // EvalComparison JSON
	CreatedMS  int64  `gorm:"column:created_ms"`                  // 报告生成时间，UTC 毫秒
}

func (EvalComparisonRecord) TableName() string { return "eval_comparison" }
