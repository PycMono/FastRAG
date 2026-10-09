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

	fx.Provide(NewEmbedding),
	fx.Provide(NewRerank),
)

// NewRerank 根据配置决定返回真实 rerank 实现还是空实现。
//
// enabled=false 时保持向后兼容：未配置 rerank 服务的服务器仍能启动，
// 搜索接口只是不执行重排，行为与升级前一致。
func NewRerank(conf *config.Config) interfaces.IRerank {
	if !conf.Rerank.Enabled {
		return nopRerank{}
	}
	return NewRerankImpl(conf)
}

// nopRerank 未启用 rerank 时的空实现：原样返回候选顺序。
type nopRerank struct{}

func (nopRerank) Rerank(ctx context.Context, query string, cands []interfaces.RerankCandidate, topN int) ([]int, error) {
	out := make([]int, len(cands))
	for i := range out {
		out[i] = i
	}
	return out, nil
}

// 编译期断言：实现必须满足端口。
var (
	_ interfaces.IEmbedding = (*Embedding)(nil)
	_ interfaces.IRerank    = (*nopRerank)(nil)
	_ interfaces.IRerank    = (*Rerank)(nil)
)
