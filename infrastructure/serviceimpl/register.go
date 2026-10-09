package serviceimpl

import (
	"context"

	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"go.uber.org/fx"
)

// Register 注册 serviceimpl 层组件。
//
// embedding 与 rerank 都注册成"注册表"而不是单个实现：配置里可以同时存在
// 多组服务商，应用层在请求入口按名字取用。
//
// 没有限流装饰器。本期不做限流（A4.9），embedding 直接暴露给调用方，
// 唯一的背压是批量导入那个固定 4 并发的工作池（§5.5）。
var Register = fx.Options(
	fx.Provide(func(conf *config.Config) (repository.IIDService, error) {
		svc, err := NewIDService(int64(conf.SnowflakeNodeID))
		if err != nil {
			return nil, err
		}
		return svc, nil
	}),

	fx.Provide(NewEmbeddingRegistry),
	fx.Provide(NewRerankRegistry),
)

// nopRerank 未启用 rerank 时的空实现：原样返回候选顺序。
//
// 分数一律给 NoRerankScore——调用方据此保留自己原有的融合分，
// 于是"没配 rerank"和"以前没做 rerank"在响应里逐字节一致。
type nopRerank struct{}

func (nopRerank) Rerank(ctx context.Context, query string, cands []interfaces.RerankCandidate, topN int) ([]interfaces.ScoredIndex, error) {
	out := make([]interfaces.ScoredIndex, len(cands))
	for i := range out {
		out[i] = interfaces.ScoredIndex{Index: i, Score: interfaces.NoRerankScore}
	}
	return out, nil
}

// 编译期断言：实现必须满足端口。
var (
	_ interfaces.IEmbedding         = (*Embedding)(nil)
	_ interfaces.IEmbeddingRegistry = (*embeddingRegistry)(nil)
	_ interfaces.IRerank            = (*Rerank)(nil)
	_ interfaces.IRerank            = (*nopRerank)(nil)
	_ interfaces.IRerankRegistry    = (*rerankRegistry)(nil)
)
