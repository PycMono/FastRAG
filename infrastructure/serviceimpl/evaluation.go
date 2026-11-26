package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// EvalHTTP 是评分器的 HTTP 客户端，只允许配置指定的地址。
type EvalHTTP struct {
	Client   *http.Client
	ScoreURL string
	Token    string

	// Judge 在评分请求时读取，页面保存后生效；不缓存或持久化到运行结果。
	Judge func() (config.EvaluationJudge, error)
}

// doJSON 通过共享 OpenAI 兼容传输层发送 JSON，并限制响应大小。
func (h *EvalHTTP) doJSON(ctx context.Context, endpoint string, input, output any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}

	body, status, err := postJSON(
		ctx,
		h.Client,
		endpoint,
		h.Token,
		payload,
		32<<20,
		apperrors.ErrEvaluationFailed,
	)
	if err != nil {
		return err
	}
	if status == http.StatusTooManyRequests {
		return interfaces.ErrEvalServiceBusy
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		switch status {
		case 401, 403:
			return fmt.Errorf("评分服务认证失败（HTTP %d），请检查内部服务 token", status)
		case 503:
			return fmt.Errorf("评分服务尚未就绪或未配置评估模型（HTTP 503）")
		case 422:
			return fmt.Errorf("评分请求未通过校验（HTTP 422），请检查输入和模型端点")
		case 504:
			return fmt.Errorf("评分服务执行超时（HTTP 504），模型可能已计费，不自动重试")
		default:
			return fmt.Errorf("评测上游 HTTP %d", status)
		}
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("评分服务响应不是合法 JSON")
	}
	return nil
}

// Generate 调用评分模型生成候选题。
func (h *EvalHTTP) Generate(
	ctx context.Context,
	source string,
) (entity.EvalGeneratedQuestion, error) {
	var output entity.EvalGeneratedQuestion
	judge, err := h.Judge()
	if err != nil {
		return output, err
	}
	input := struct {
		Source string                 `json:"source"`
		Judge  config.EvaluationJudge `json:"judge"`
	}{Source: source, Judge: judge}

	err = h.doJSON(
		ctx,
		strings.TrimRight(h.ScoreURL, "/")+"/generate",
		input,
		&output,
	)
	return output, err
}

// ScoreBatch 调用内部批量评分器，校验响应归属和逐指标评分。
func (h *EvalHTTP) ScoreBatch(
	ctx context.Context,
	inputs []entity.EvalScoreInput,
) ([]entity.EvalScoreOutput, error) {
	if len(inputs) == 0 {
		return []entity.EvalScoreOutput{}, nil
	}

	payload := struct {
		Samples []entity.EvalScoreInput `json:"samples"`
		Judge   *config.EvaluationJudge `json:"judge,omitempty"`
	}{Samples: inputs}
	if h.Judge != nil {
		judge, err := h.Judge()
		if err != nil {
			return nil, err
		}
		payload.Judge = &judge
	}

	var response struct {
		Results []entity.EvalScoreOutput `json:"results"`
	}
	endpoint := strings.TrimRight(h.ScoreURL, "/") + "/evaluate-batch"
	if err := h.doJSON(ctx, endpoint, payload, &response); err != nil {
		return nil, err
	}
	if len(response.Results) != len(inputs) {
		return nil, fmt.Errorf("批量评分结果数量不符")
	}

	expected := make(map[string]entity.EvalScoreInput, len(inputs))
	for _, input := range inputs {
		if _, exists := expected[input.RequestID]; exists {
			return nil, fmt.Errorf("评分请求 ID 重复")
		}
		expected[input.RequestID] = input
	}

	for _, output := range response.Results {
		input, exists := expected[output.RequestID]
		if !exists {
			return nil, fmt.Errorf("评分结果请求 ID 未知")
		}
		if err := validateScoreOutput(input, output); err != nil {
			return nil, err
		}
		delete(expected, output.RequestID)
	}
	if len(expected) > 0 {
		return nil, fmt.Errorf("评分结果缺少样本")
	}
	return response.Results, nil
}

func validateScoreOutput(
	input entity.EvalScoreInput,
	output entity.EvalScoreOutput,
) error {
	if output.RequestID != input.RequestID || len(output.Metrics) != len(input.Metrics) {
		return fmt.Errorf("评分关联 ID 或指标数量不符")
	}

	expectedMetrics := make(map[string]bool, len(input.Metrics))
	for _, name := range input.Metrics {
		expectedMetrics[name] = true
	}

	for _, metric := range output.Metrics {
		if !expectedMetrics[metric.Name] {
			return fmt.Errorf("评分指标未知或重复")
		}
		delete(expectedMetrics, metric.Name)

		switch metric.Status {
		case "ok":
			if metric.Value == nil ||
				math.IsNaN(*metric.Value) ||
				math.IsInf(*metric.Value, 0) ||
				*metric.Value < 0 || *metric.Value > 1 {
				return fmt.Errorf("评分值无效")
			}
		case "error", "timeout", "not_applicable", "cancelled":
			if metric.Value != nil {
				return fmt.Errorf("失败指标不能有评分值")
			}
		default:
			return fmt.Errorf("未知评分状态")
		}
	}
	return nil
}
