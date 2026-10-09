package serviceimpl

import (
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

	fx.Provide(NewOpenAIEmbedding),
	fx.Provide(NewRerankStub),
)

// 编译期断言：实现必须满足端口。
var (
	_ interfaces.IEmbeddingService = (*OpenAIEmbedding)(nil)
	_ interfaces.IRerankService    = (*RerankStub)(nil)
)
