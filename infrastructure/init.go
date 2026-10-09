package infrastructure

import (
	"context"
	"fmt"

	"github.com/PycMono/FastRAG/application/service"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/domain/value_object"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/controller"
	"github.com/PycMono/FastRAG/infrastructure/driver/es"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/FastRAG/infrastructure/driver/mysql"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

// Init 初始化 HTTP 服务所需的全部组件。
func Init(conf *config.Config) fx.Option {
	return fx.Options(
		core(conf),

		fx.Provide(gingext.NewEngine),
		fx.Provide(gingext.NewHTTPServer),
		controller.Register,
	)
}

// InitCLI 只装配命令行任务需要的部分，不启 HTTP Server。
//
// 单独开一个而不是复用 Init：Init 里 controller.Register 会注册路由
// 并把 HTTP 服务拉起来，批处理任务不需要、也不该占着端口。
func InitCLI(conf *config.Config) fx.Option {
	return core(conf)
}

// core 三种入口（HTTP / CLI / 测试）共用的装配。
func core(conf *config.Config) fx.Option {
	return fx.Options(
		fx.Supply(conf),

		fx.Provide(mysql.NewProvider),

		// TransProvider 同时满足两个接口，按接口分别暴露：
		// 仓储要 sqlsdk.Provider（UseDB 是事务感知的，事务里自动用同一个连接），
		// 应用层和 NewKnowledgeDocRepo 都要 transaction.Manager
		//（Save 的「锁行 + upsert」必须在一个事务里，见 A4.4）
		fx.Provide(func(p *sqlsdk.TransProvider) sqlsdk.Provider { return p }),
		fx.Provide(func(p *sqlsdk.TransProvider) transaction.Manager { return p }),

		fx.Provide(es.NewClient),

		// 配置 → 领域调参。
		// 在这里转一次手，application 层就不必 import infrastructure/config（§3）
		fx.Provide(func(c *config.Config) value_object.SearchTuning {
			return value_object.SearchTuning{
				BM25Top:       c.Search.BM25Top,
				KNNTops:       c.Search.KNNTops,
				NumCandidates: c.Search.NumCandidates,
				RankConstant:  c.Search.RankConstant,
				DenseWeight:   c.Search.DenseWeight,
			}
		}),

		persistence.Register,
		serviceimpl.Register,
		domain.Register,
		service.Register,

		fx.Invoke(checkTrustRequestAccount),
		fx.Invoke(ensureIndexOnStart),
	)
}

// checkTrustRequestAccount 是一道防误部署的闸门（§6.1）。
//
// 本服务不做鉴权——account 就是请求体里的一个普通字符串参数。
// 能构造请求体的人就能读写任意租户的数据。这个设计只有在「服务只跑在可信内网、
// 由调用方自己完成对最终用户的鉴权」这个前提下才成立。
//
// 问题在于，把服务直接摆到公网上是个太容易犯的错——改一行 nginx 配置就够了，
// 而代码本身不会报任何错。所以这里把前提变成一个必须显式承认的开关：
// 没配 trust_request_account = true 就拒绝启动。
//
// 用 fx.Invoke 而不是放在 main 里手写 if：这样 CLI 入口（InitCLI）也会过这道闸门，
// 三个入口一个都漏不掉。
func checkTrustRequestAccount(conf *config.Config) error {
	if !conf.Security.TrustRequestAccount {
		return apperrors.NewSysError(apperrors.CodeInternal,
			"拒绝启动：security.trust_request_account 未置为 true。\n"+
				"本服务不做鉴权，account 直接取自请求体，任何能构造请求的人都能读写任意租户。\n"+
				"确认它只对内网可信调用方开放（且调用方已自行完成用户鉴权）后，"+
				"在配置里显式写上 true。")
	}
	return nil
}

// ensureIndexOnStart 启动期把 ES 索引建好（§9.4）。
//
// 为什么放在启动期而不是写入路径：详见 §9.4 那张表。一句话版本——
// 写入路径建索引意味着「索引已存在」这个大前提下的代码从没在上线路径上跑过，
// 而恰恰是那条路径承载全部真实流量。
//
// 用 OnStart 而不是直接 Invoke：OnStart 是 fx 的生命周期回调，
// 失败会让 app.Run() 直接返回错误（进程起不来），正是我们要的——
// 索引建不出来的服务，起来也是个只会报错的空壳。
func ensureIndexOnStart(lc fx.Lifecycle, store knowledgerepo.IVectorStore) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := store.EnsureIndex(ctx); err != nil {
				return fmt.Errorf("启动期建索引失败: %w", err)
			}
			logsdk.Info(ctx, "ES 索引就绪")
			return nil
		},
	})
}
