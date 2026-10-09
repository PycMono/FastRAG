package value_object

import (
	"strings"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// SearchOptions 检索参数。
type SearchOptions struct {
	Query       string
	Limit       int
	BizTags     []string
	DenseWeight float64
	Rerank      bool
}

// Normalize 补齐默认值并校验。
func (o SearchOptions) Normalize() (SearchOptions, error) {
	out := o

	out.Query = strings.TrimSpace(out.Query)
	if out.Query == "" {
		return out, apperrors.NewParamError("query 不能为空")
	}

	if out.Limit == 0 {
		out.Limit = constants.DefaultSearchLimit
	}
	if out.Limit < 1 || out.Limit > constants.MaxSearchLimit {
		return out, apperrors.NewParamError("limit 必须在 [1, 100]")
	}

	if out.DenseWeight < 0 || out.DenseWeight > 1 {
		return out, apperrors.NewParamError("dense_weight 必须在 [0, 1]")
	}

	return out, nil
}

// UseBM25 / UseKNN 判断两路各自要不要发。
// 阈值不是 0/1 而是 ±0.01，是为了容忍配置里写的 0.999 这类值。
func (o SearchOptions) UseBM25() bool { return o.DenseWeight <= 0.99 }
func (o SearchOptions) UseKNN() bool  { return o.DenseWeight >= 0.01 }
