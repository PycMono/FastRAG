package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	evalsvc "github.com/PycMono/FastRAG/application/service/evaluation"
	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	"go.uber.org/fx"
)

// newEvaluation 仅在 HTTP 入口装配，不改变原 core 的检索依赖。
func newEvaluation(conf *config.Config, lifecycle fx.Lifecycle) (*evalsvc.Service, error) {
	cfg := conf.Evaluation.WithDefaults()
	files := &persistence.EvalFiles{}
	if cfg.Enabled {
		if len(os.Getenv("FASTRAG_EVAL_TOKEN")) < 24 {
			return nil, fmt.Errorf("FASTRAG_EVAL_TOKEN 至少 24 位")
		}

		var err error
		files, err = openEvaluationFiles(cfg)
		if err != nil {
			return nil, err
		}
	}

	client := &serviceimpl.EvalHTTP{
		Client: &http.Client{
			Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		SearchURL: cfg.SearchBaseURL,
		ScoreURL:  cfg.ScorerBaseURL,
		Token:     os.Getenv("FASTRAG_EVAL_TOKEN"),
		Judge:     loadEvaluationJudge,
	}
	options := evalsvc.Options{
		Enabled:    cfg.Enabled,
		RunTimeout: time.Duration(cfg.RunTimeoutSeconds) * time.Second,
		Metadata: map[string]any{
			"git_commit":     cfg.GitCommit,
			"environment":    cfg.Environment,
			"dirty_worktree": "unknown",
			"owner":          "server-single-instance",
		},
	}
	service := evalsvc.NewService(
		&persistence.EvalDatasets{Files: files},
		&persistence.EvalRuns{Files: files},
		client,
		client,
		options,
	)

	lifecycle.Append(fx.Hook{
		OnStart: service.Start,
		OnStop: func(ctx context.Context) error {
			if err := service.Stop(ctx); err != nil {
				return err
			}
			if cfg.Enabled {
				return files.Close()
			}
			return nil
		},
	})
	return service, nil
}

func newEvaluationReview(tasks *evalsvc.Service, conf *config.Config, lifecycle fx.Lifecycle) (*evalsvc.ReviewService, error) {
	cfg := conf.Evaluation.WithDefaults()
	// 文件根单独打开，避免复核服务依赖任务执行器的内部状态。
	files := &persistence.EvalFiles{}
	if cfg.Enabled {
		var err error
		files, err = openEvaluationFiles(cfg)
		if err != nil {
			return nil, err
		}
	}
	client := &serviceimpl.EvalHTTP{
		Client: &http.Client{
			Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		SearchURL: cfg.SearchBaseURL,
		ScoreURL:  cfg.ScorerBaseURL,
		Token:     os.Getenv("FASTRAG_EVAL_TOKEN"),
		Judge:     loadEvaluationJudge,
	}
	service := evalsvc.NewReviewService(tasks, client, &persistence.EvalReviews{Files: files})
	service.ConfigureAutomaticGeneration(cfg.AutoGenerate, cfg.AutoSampleCount)
	lifecycle.Append(fx.Hook{
		OnStart: service.Start,
		OnStop: func(ctx context.Context) error {
			if err := service.Stop(ctx); err != nil {
				return err
			}
			if cfg.Enabled {
				return files.Close()
			}
			return nil
		},
	})
	return service, nil
}

func connectEvaluationLifecycle(imports *ingest.Service, review *evalsvc.ReviewService) {
	imports.SetEvaluationLifecycle(review)
}

func openEvaluationFiles(cfg config.EvaluationConfig) (*persistence.EvalFiles, error) {
	dataPath, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	runPath, err := filepath.Abs(cfg.RunDir)
	if err != nil {
		return nil, err
	}
	if dataPath == runPath {
		return nil, fmt.Errorf("评测数据集与运行目录不能相同")
	}
	if err := os.MkdirAll(runPath, 0700); err != nil {
		return nil, err
	}

	data, err := os.OpenRoot(dataPath)
	if err != nil {
		return nil, err
	}
	runs, err := os.OpenRoot(runPath)
	if err != nil {
		data.Close()
		return nil, err
	}
	return &persistence.EvalFiles{Data: data, Runs: runs}, nil
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
