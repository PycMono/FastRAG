package search

import (
	"strings"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// searchOptions 检索参数。
//
// 刻意**不**放进 domain：它一次都没跨过包边界——resolveOptions 产出、
// buildReq 消费，两端都在本包内；再往下 store.Search 收的是仓储层的
// VectorSearchReq，到那一步它已经被拍平。放外面只会让它假装自己有跨层含义。
//
// 对照 ChunkOptions / ChunkInput：它们也没有自己的包，就住在 domain/service/
// 里挨着 Splitter——因为 splitter.go 的函数签名要它们，而领域层够不着
// （也不该够着）common/dto。
type searchOptions struct {
	Query       string
	Limit       int
	BizTags     []string
	DenseWeight float64
	Rerank      bool
}

// normalize 补齐默认值并校验。
func (o searchOptions) normalize() (searchOptions, error) {
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

// useKNN 判断向量路要不要发。不发就省下这次 embedding 调用（见 Service.Search ②）。
//
// 没有配对的 useBM25：BM25 发不发由仓储层自己看 DenseWeight 决定
// （值随 VectorSearchReq 带过去了，见 vector_store_es.go 的 runSearch），
// 应用层在请求组装之前判一次没有任何效果——原来那个 UseBM25 就一直没人调。
//
// 阈值不是 0/1 而是 ±0.01，是为了容忍配置里写的 0.999 这类值。
func (o searchOptions) useKNN() bool { return o.DenseWeight >= 0.01 }
