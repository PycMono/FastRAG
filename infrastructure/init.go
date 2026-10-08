package infrastructure

import (
	"github.com/PycMono/FastRAG/application/service"
	"github.com/PycMono/FastRAG/domain"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/controller"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/FastRAG/infrastructure/driver/mysql"
	"github.com/PycMono/FastRAG/infrastructure/driver/redis"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	"go.uber.org/fx"
)

// Init 初始化所有基础设施组件
func Init(conf *config.Config) fx.Option {
	return fx.Options(
		// 提供配置
		fx.Supply(conf),

		// 提供 Redis 客户端（go-cache-sdk，addr 为空则不启用）
		fx.Provide(redis.NewClient),

		// 提供 MySQL Provider（go-mysql-sdk）
		fx.Provide(mysql.NewProvider),

		// 提供 Gin 引擎和 HTTP Server（go-gin-sdk）
		fx.Provide(gingext.NewEngine),
		fx.Provide(gingext.NewHTTPServer),

		// 注册控制器（HTTP 入口）
		controller.Register,

		// 注册应用服务
		service.Register,

		// 注册领域层
		domain.Register,

		// 注册持久化实现
		persistence.Register,

		// 注册基础设施服务（ID 生成器等）
		serviceimpl.Register,
	)
}
