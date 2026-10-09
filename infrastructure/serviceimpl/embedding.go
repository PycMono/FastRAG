// 本文件实现向量化：一个实现，两处形状开关。
//
// 分批、按下标回填、维度校验全部共用，一行都不分叉；真正随协议变的只有
// "请求体怎么拼"（buildBody）和"响应从哪取向量、下标字段叫什么"（parseVectors）。
//
//	openai    POST {url}                       body {model, input:[...]}
//	                                          响应 data[].{index, embedding}
//	dashscope POST {url}（原生 /api/v1/services/...）
//	                                           body {model, input:{texts}, parameters:{text_type}}
//	                                          响应 output.embeddings[].{text_index, embedding}
//
// ⚠️ 两个协议的下标字段名不一样：openai 是 index，dashscope 是 text_index。
// 形状看着像，抄错一个就是把所有向量挂到第 0 条上，而且不报错（设计文档 §4.1）。

package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// maxEmbedResponseBytes 响应体读取上限。
// 一批 32 条 × 1024 维的 float32 JSON 大约 1MB，64MB 已经非常宽裕，
// 但没有上限地 ReadAll 迟早会被一个畸形响应打爆内存。
const maxEmbedResponseBytes = 64 << 20

// text_type 是 dashscope 协议的一部分，不是可选装饰：它替代了 openai 那边
// 靠字符串前缀实现的 query/doc 区分（§4.2）。
const (
	textTypeQuery    = "query"
	textTypeDocument = "document"
)

// Embedding 向量化实现。
//
// 文件名与类型名不带厂商名：同一个实现靠 protocol 开关对接所有服务商。
type Embedding struct {
	client      *http.Client
	protocol    string
	url         string // 完整端点，不再拼 /embeddings 后缀
	apiKey      string
	model       string
	dim         int
	batchSize   int
	queryPrefix string
}

// newEmbedding 从一份已合并好的参数构造实现。
//
// 只有注册表调它——"继承外层"在 Params() 里就填完了，这里不再判 0。
func newEmbedding(p config.EmbeddingParams) *Embedding {
	return &Embedding{
		client:      &http.Client{Timeout: time.Duration(p.TimeoutMS) * time.Millisecond},
		protocol:    p.Protocol,
		url:         p.URL,
		apiKey:      p.APIKey,
		model:       p.Model,
		dim:         p.Dim,
		batchSize:   p.BatchSize,
		queryPrefix: p.QueryPrefix,
	}
}

func (e *Embedding) Dim() int      { return e.dim }
func (e *Embedding) Model() string { return e.model }

// EmbedDocs 批量编码文档，自动按 batch_size 分片。
//
// 返回顺序与入参严格一一对应：**按响应里的 index 回填，不信返回数组的顺序**。
// 顺序错位是最隐蔽的一类 bug——向量都在、条数也对，只是挂到了错误的切片上，
// 检索结果全错但看起来一切正常。
//
// 空文本（含纯空白）**不发给上游**，返回值留 nil，但绝不改变返回的条数——
// 下游 factory.BuildVectorDocs 会拿条数跟切片数对齐，少一条就 panic。
//
// 为什么非要挑出来：DashScope 收到 texts 里任何一个空串，**整批**都会改用一个
// 别的模型、返回 2560 维（实测 2026-10-09，只有 "" 触发，空白串不触发），然后
// 被下面那道维度检查拦下——报的是"维度为 2560"，完全指不到真正的原因。
// 而 format=text 的切片标题恒为空（splitter.go 传的就是空串），也就是纯文本
// 导入必然踩中；format=chunks 只要有一片没给 title 同样如此。Ollama 对空串
// 老老实实返回 1024，所以这个坑一直藏着，换到通义才露出来。
func (e *Embedding) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// 先挑出真正需要算的下标，非空的按原顺序批量发出去，算完再按下标散布回 out。
	wanted := make([]int, 0, len(texts))
	for i, t := range texts {
		if strings.TrimSpace(t) != "" {
			wanted = append(wanted, i)
		}
	}

	out := make([][]float32, len(texts))
	for start := 0; start < len(wanted); start += e.batchSize {
		end := start + e.batchSize
		if end > len(wanted) {
			end = len(wanted)
		}

		batch := make([]string, 0, end-start)
		for _, i := range wanted[start:end] {
			batch = append(batch, texts[i])
		}

		vecs, err := e.embed(ctx, batch, textTypeDocument)
		if err != nil {
			return nil, err
		}
		for j, i := range wanted[start:end] {
			out[i] = vecs[j]
		}
	}

	// 剩下没被填上的就是空文本，留 nil——序列化成 null 后 ES 当作字段不存在。
	//
	// 为什么不补零向量：ES 的 cosine 相似度**明确拒绝零模长向量**
	// （实测 document_parsing_exception: "The [cosine] similarity does not
	// support vectors with zero magnitude"），补了会让整批 bulk 全部 400，
	// 比原来的问题还难查。缺失字段则是合法的。
	//
	// 语义上这也更对：没有标题就不该有标题向量。该切片自然不会出现在
	// title_vec 的 kNN 结果里——它本来也不该被标题检索召回。
	return out, nil
}

// EmbedQuery 编码查询串，带非对称前缀。
//
// 必须与 EmbedDocs 分开：bge-m3 / E5 / GTE 这类模型检索时要求给 query
// 加指令前缀（如 "query: "），doc 侧不加。混用会显著掉分，
// 而且不会报错——只是效果变差，很难归因。
func (e *Embedding) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{e.queryPrefix + text}, textTypeQuery)
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
			fmt.Sprintf("embedding 返回 %d 条向量，期望 1 条", len(vecs)))
	}
	return vecs[0], nil
}

// embedResponse 两种协议的响应。data 是 openai 的，output.embeddings 是 dashscope 的；
// 按协议只认其中一个，另一个留空。
type embedResponse struct {
	apiError
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Output *struct {
		Embeddings []struct {
			TextIndex int       `json:"text_index"`
			Embedding []float32 `json:"embedding"`
		} `json:"embeddings"`
	} `json:"output"`
}

// buildBody 按协议拼请求体。
//
// textType 只有 dashscope 用得上（协议自带 query/document 之分）；openai 那边
// query 与 doc 的区别靠 queryPrefix 字符串前缀，见 EmbedQuery。
func (e *Embedding) buildBody(texts []string, textType string) any {
	if e.protocol == config.ProtocolDashScope {
		return map[string]any{
			"model": e.model,
			"input": map[string]any{"texts": texts},
			"parameters": map[string]any{
				"text_type": textType,
			},
		}
	}
	return map[string]any{"model": e.model, "input": texts}
}

// parseVectors 按协议取向量并按下标回填。
//
// ⚠️ 两种协议的向量结构长得几乎一样，下标字段名却不同（index / text_index）。
// 正因为像，才必须分成两条分支写死——共用一个解析器就会把所有向量都挂到第 0 条上。
func (e *Embedding) parseVectors(parsed *embedResponse, n int) [][]float32 {
	out := make([][]float32, n)

	if e.protocol == config.ProtocolDashScope {
		if parsed.Output != nil {
			for _, d := range parsed.Output.Embeddings {
				if d.TextIndex >= 0 && d.TextIndex < n {
					out[d.TextIndex] = d.Embedding
				}
			}
		}
		return out
	}

	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < n {
			out[d.Index] = d.Embedding
		}
	}
	return out
}

func (e *Embedding) embed(ctx context.Context, texts []string, textType string) ([][]float32, error) {
	payload, err := json.Marshal(e.buildBody(texts, textType))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	body, status, err := postJSON(ctx, e.client, e.url, e.apiKey,
		payload, maxEmbedResponseBytes, apperrors.ErrEmbeddingFailed)
	if err != nil {
		return nil, err
	}

	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 响应不是合法 JSON，HTTP %d: %s", status, truncateBody(body, 512)))
	}
	if err := parsed.apiError.check(status, apperrors.CodeEmbeddingFail, "embedding", body); err != nil {
		return nil, err
	}

	out := e.parseVectors(&parsed, len(texts))

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
