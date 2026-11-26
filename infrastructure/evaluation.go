package infrastructure

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/PycMono/FastRAG/application/service/docview"
	evalsvc "github.com/PycMono/FastRAG/application/service/evaluation"
	"github.com/PycMono/FastRAG/application/service/ingest"
	appsearch "github.com/PycMono/FastRAG/application/service/search"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	"go.uber.org/fx"
)

// newEvaluation 仅在 HTTP 入口装配，不改变原 core 的检索依赖。
func newEvaluation(
	conf *config.Config,
	provider sqlsdk.Provider,
	search *appsearch.Service,
	lifecycle fx.Lifecycle,
) (*evalsvc.Service, error) {
	cfg := conf.Evaluation.WithDefaults()
	if cfg.Enabled && len(os.Getenv("FASTRAG_EVAL_TOKEN")) < 24 {
		return nil, fmt.Errorf("FASTRAG_EVAL_TOKEN 至少 24 位")
	}

	client := evaluationClient(cfg)
	service := evalsvc.NewService(
		persistence.NewEvalDatasetRepo(provider),
		persistence.NewEvalRunRepo(provider),
		search,
		client,
		evalsvc.Options{
			Enabled:    cfg.Enabled,
			RunTimeout: time.Duration(cfg.RunTimeoutSeconds) * time.Second,
			Retention:  time.Duration(cfg.RetentionDays) * 24 * time.Hour,
			Metadata: map[string]any{
				"git_commit":     cfg.GitCommit,
				"environment":    cfg.Environment,
				"dirty_worktree": "unknown",
				"owner":          "server-single-instance",
			},
		},
	)

	lifecycle.Append(fx.Hook{OnStart: service.Start, OnStop: service.Stop})
	return service, nil
}

func newEvaluationReview(
	tasks *evalsvc.Service,
	conf *config.Config,
	provider sqlsdk.Provider,
	tm transaction.Manager,
	docs *docview.Service,
	lifecycle fx.Lifecycle,
) (*evalsvc.DatasetService, error) {
	cfg := conf.Evaluation.WithDefaults()
	service := evalsvc.NewDatasetService(
		tasks,
		docs, evaluationClient(cfg),
		persistence.NewEvalReviewRepo(provider, tm),
	)
	service.ConfigureAutomaticGeneration(cfg.AutoGenerate, cfg.AutoSampleCount)

	lifecycle.Append(fx.Hook{OnStart: service.Start, OnStop: service.Stop})
	return service, nil
}

func evaluationClient(cfg config.EvaluationConfig) *serviceimpl.EvalHTTP {
	return &serviceimpl.EvalHTTP{
		Client: &http.Client{
			Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		ScoreURL: cfg.ScorerBaseURL,
		Token:    os.Getenv("FASTRAG_EVAL_TOKEN"),
		Judge:    loadEvaluationJudge,
	}
}

func connectEvaluationLifecycle(imports *ingest.Service, review *evalsvc.DatasetService) {
	imports.SetEvaluationLifecycle(review)
}

// loadEvaluationJudge 配置保存采用原子替换；密钥不进入评测任务快照。
func loadEvaluationJudge() (config.EvaluationJudge, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return config.EvaluationJudge{}, fmt.Errorf("无法读取评估模型配置")
	}

	var current config.Config
	if err := json.Unmarshal(data, &current); err != nil {
		return config.EvaluationJudge{}, fmt.Errorf("评估模型配置无效")
	}
	judge := current.Evaluation.Judge
	if judge.APIKey == "" || judge.Model == "" || judge.BaseURL == "" {
		return judge, fmt.Errorf("请先在配置管理中填写评估模型")
	}
	return judge, nil
}
