// 本文件用 OpenAI 兼容协议对接向量化服务。
//
// 兼容面很宽：OpenAI / 火山引擎 / 通义 / vLLM / Ollama / 自建 model_proxy
// 都是 POST {base_url}/embeddings，请求体 {model, input}。
// 协议这么简单，引第三方 SDK 反而是给自己加一层要跟着升级的依赖。
package serviceimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// maxEmbedResponseBytes 响应体读取上限。
// 一批 32 条 × 1024 维的 float32 JSON 大约 1MB，64MB 已经非常宽裕，
// 但没有上限地 ReadAll 迟早会被一个畸形响应打爆内存。
const maxEmbedResponseBytes = 64 << 20

// OpenAIEmbedding OpenAI 兼容协议的向量化实现。
type OpenAIEmbedding struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	model       string
	dim         int
	batchSize   int
	queryPrefix string
}

func NewOpenAIEmbedding(conf *config.Config) interfaces.IEmbedding {
	c := conf.Embedding

	timeout := time.Duration(c.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	batch := c.BatchSize
	if batch <= 0 {
		batch = 32
	}

	return &OpenAIEmbedding{
		client:      &http.Client{Timeout: timeout},
		baseURL:     strings.TrimRight(c.BaseURL, "/"),
		apiKey:      c.APIKey,
		model:       c.Model,
		dim:         c.Dim,
		batchSize:   batch,
		queryPrefix: c.QueryPrefix,
	}
}

func (e *OpenAIEmbedding) Dim() int      { return e.dim }
func (e *OpenAIEmbedding) Model() string { return e.model }

// EmbedDocs 批量编码文档，自动按 batch_size 分片。
//
// 返回顺序与入参严格一一对应：**按响应里的 index 回填，不信返回数组的顺序**。
// 顺序错位是最隐蔽的一类 bug——向量都在、条数也对，只是挂到了错误的切片上，
// 检索结果全错但看起来一切正常。
func (e *OpenAIEmbedding) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += e.batchSize {
		end := start + e.batchSize
		if end > len(texts) {
			end = len(texts)
		}

		vecs, err := e.embed(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedQuery 编码查询串，带非对称前缀。
//
// 必须与 EmbedDocs 分开：bge-m3 / E5 / GTE 这类模型检索时要求给 query
// 加指令前缀（如 "query: "），doc 侧不加。混用会显著掉分，
// 而且不会报错——只是效果变差，很难归因。
func (e *OpenAIEmbedding) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{e.queryPrefix + text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
			fmt.Sprintf("embedding 返回 %d 条向量，期望 1 条", len(vecs)))
	}
	return vecs[0], nil
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedding) embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(map[string]any{"model": e.model, "input": texts})
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponseBytes))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 响应不是合法 JSON，HTTP %d: %s", resp.StatusCode, truncate(body, 512)))
	}
	if parsed.Error != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
			"embedding 服务返回错误: "+parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 服务 HTTP %d: %s", resp.StatusCode, truncate(body, 512)))
	}

	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			continue
		}
		out[d.Index] = d.Embedding
	}

	for i, v := range out {
		if len(v) == 0 {
			return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
				fmt.Sprintf("embedding 第 %d 条缺失", i))
		}
		// 维度对不上必须在这里就拦下：等写进 ES 才发现，
		// 报的会是 dense_vector 的 mapping 错误，离真正的原因很远
		if e.dim > 0 && len(v) != e.dim {
			return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
				"embedding 维度为 %d，配置里写的是 %d；两者必须一致", len(v), e.dim))
		}
	}
	return out, nil
}

func truncate(body []byte, n int) string {
	runes := []rune(strings.TrimSpace(string(body)))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "...(截断)"
}
