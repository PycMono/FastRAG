package config

import (
	"fmt"
	"net/url"
)

// EvaluationConfig 不影响正常检索，默认关闭；评估模型凭据由管理配置保存。
type EvaluationConfig struct {
	AutoGenerate          bool            `json:"auto_generate"`
	AutoSampleCount       int             `json:"auto_sample_count"`
	Judge                 EvaluationJudge `json:"judge"`
	Enabled               bool            `json:"enabled"`
	RetentionDays         int             `json:"retention_days"`
	ScorerBaseURL         string          `json:"scorer_base_url"`
	RequestTimeoutSeconds int             `json:"request_timeout_seconds"`
	RunTimeoutSeconds     int             `json:"run_timeout_seconds"`
	Environment           string          `json:"environment"`
	GitCommit             string          `json:"git_commit"`
}

// EvaluationJudge 只由后端发给内部评分器，不写入评测任务快照。
type EvaluationJudge struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
}

func (c EvaluationConfig) WithDefaults() EvaluationConfig {
	if c.AutoSampleCount == 0 {
		c.AutoSampleCount = 50
	}
	if c.RetentionDays == 0 {
		c.RetentionDays = 30
	}
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = 240
	}
	if c.RunTimeoutSeconds == 0 {
		c.RunTimeoutSeconds = 3600
	}
	return c
}

func (c EvaluationConfig) validate() error {
	if !c.Enabled {
		return nil
	}

	c = c.WithDefaults()
	if c.AutoSampleCount < 3 || c.AutoSampleCount > 100 {
		return fmt.Errorf("自动出题数量须为 3–100")
	}
	if c.RetentionDays < 1 || c.RetentionDays > 3650 {
		return fmt.Errorf("评测保留天数须为 1–3650")
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 600 ||
		c.RunTimeoutSeconds < 1 || c.RunTimeoutSeconds > 10800 {
		return fmt.Errorf("evaluation 请求超时须为 1–600 秒，任务超时须为 1–10800 秒")
	}

	parsedURL, err := url.Parse(c.ScorerBaseURL)
	if err != nil || parsedURL == nil ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") ||
		parsedURL.Host == "" || parsedURL.User != nil ||
		parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("评分服务地址必须为无凭据的 HTTP(S) 地址")
	}

	return nil
}
