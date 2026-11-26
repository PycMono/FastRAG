package serviceimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// EvalHTTP 只允许部署配置指定的地址，不接受页面传入上游 URL。
type EvalHTTP struct {
	Client    *http.Client
	SearchURL string
	ScoreURL  string
	Token     string

	// Judge 在评分请求时读取，页面保存后生效；不缓存或持久化到运行结果。
	Judge func() (config.EvaluationJudge, error)
}

// post 发送 JSON 请求，并限制上游响应大小。
func (h *EvalHTTP) post(ctx context.Context, endpoint, token string, input, output any) error {
	requestBody, err := json.Marshal(input)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := h.Client.Do(request)
	if err != nil {
		return fmt.Errorf("评测上游请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return interfaces.ErrEvalServiceBusy
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		switch response.StatusCode {
		case 401, 403:
			return fmt.Errorf("评分服务认证失败（HTTP %d），请检查内部服务 token", response.StatusCode)
		case 503:
			return fmt.Errorf("评分服务尚未就绪或未配置评估模型（HTTP 503）")
		case 422:
			return fmt.Errorf("评分请求未通过校验（HTTP 422），请检查输入和模型端点")
		case 504:
			return fmt.Errorf("评分服务执行超时（HTTP 504），模型可能已计费，不自动重试")
		default:
			return fmt.Errorf("评测上游 HTTP %d", response.StatusCode)
		}
	}

	const maxResponseBytes = 32 << 20
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxResponseBytes {
		return fmt.Errorf("评测上游响应超过 32 MiB")
	}

	return json.Unmarshal(responseBody, output)
}

// Search 调用现有检索接口，保留调用方实际收到的结果顺序。
func (h *EvalHTTP) Search(
	ctx context.Context,
	testCase entity.EvalCase,
	profile entity.EvalProfile,
) (*entity.EvalRetrieval, error) {
	searchRequest := dto.SearchDTO{
		Tenant:         testCase.Tenant,
		KBNos:          testCase.KBNos,
		Query:          testCase.Question,
		Limit:          profile.Limit,
		RetrieveCount:  &profile.RetrieveCount,
		DenseWeight:    &profile.DenseWeight,
		RerankSwitch:   profile.RerankSwitch,
		Diagnostics:    true,
		EmbedModel:     profile.EmbedModel,
		RerankModel:    profile.RerankModel,
		MinScore:       profile.MinScore,
		FusionMinScore: profile.FusionMinScore,
	}

	var envelope struct {
		Code int                   `json:"code"`
		Data *entity.EvalRetrieval `json:"data"`
	}

	endpoint := strings.TrimRight(h.SearchURL, "/") + "/api/v1/search"
	if err := h.post(ctx, endpoint, "", searchRequest, &envelope); err != nil {
		return nil, err
	}

	if envelope.Code != 0 || envelope.Data == nil {
		return nil, fmt.Errorf("检索业务失败 code=%d", envelope.Code)
	}

	seenChunkIDs := make(map[string]bool, len(envelope.Data.Items))
	for _, hit := range envelope.Data.Items {
		if hit.ChunkID == "" || seenChunkIDs[hit.ChunkID] {
			return nil, fmt.Errorf("检索结果片段 ID 缺失或重复")
		}
		seenChunkIDs[hit.ChunkID] = true
	}

	return envelope.Data, nil
}

// Score 调用内部评分器，校验响应归属和逐指标评分。
func (h *EvalHTTP) Score(
	ctx context.Context,
	input entity.EvalScoreInput,
) (*entity.EvalScoreOutput, error) {
	var payload any = input
	if h.Judge != nil {
		judge, err := h.Judge()
		if err != nil {
			return nil, err
		}

		payload = struct {
			entity.EvalScoreInput
			Judge config.EvaluationJudge `json:"judge"`
		}{
			EvalScoreInput: input,
			Judge:          judge,
		}
	}

	var output entity.EvalScoreOutput
	endpoint := strings.TrimRight(h.ScoreURL, "/") + "/evaluate"
	for attempt := 0; ; attempt++ {
		err := h.post(ctx, endpoint, h.Token, payload, &output)
		if err == nil {
			break
		}
		// 只对已明确拒绝执行的忙碌响应等待；超时/网络错误不自动计费重试。
		if !errors.Is(err, interfaces.ErrEvalServiceBusy) || attempt >= 3 {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if output.RequestID != input.RequestID || len(output.Metrics) != len(input.Metrics) {
		return nil, fmt.Errorf("评分关联 ID 或指标数量不符")
	}

	expectedMetrics := make(map[string]bool, len(input.Metrics))
	for _, name := range input.Metrics {
		expectedMetrics[name] = true
	}

	for _, metric := range output.Metrics {
		if !expectedMetrics[metric.Name] {
			return nil, fmt.Errorf("评分指标未知或重复")
		}
		delete(expectedMetrics, metric.Name)

		switch metric.Status {
		case "ok":
			if metric.Value == nil ||
				math.IsNaN(*metric.Value) ||
				math.IsInf(*metric.Value, 0) ||
				*metric.Value < 0 || *metric.Value > 1 {
				return nil, fmt.Errorf("评分值无效")
			}
		case "error", "timeout", "not_applicable", "cancelled":
			if metric.Value != nil {
				return nil, fmt.Errorf("失败指标不能有评分值")
			}
		default:
			return nil, fmt.Errorf("未知评分状态")
		}
	}

	return &output, nil
}
