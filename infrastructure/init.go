package infrastructure

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/PycMono/FastRAG/application/service"
	"github.com/PycMono/FastRAG/application/service/search"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/controller"
	"github.com/PycMono/FastRAG/infrastructure/driver"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	ginsdk "github.com/PycMono/go-gin-sdk"
	logsdk "github.com/PycMono/go-logger-sdk"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	"go.uber.org/fx"
)

// Init 初始化 HTTP 服务所需的全部组件。
func Init(conf *config.Config) fx.Option {
	return fx.Options(
		core(conf),

		fx.Provide(driver.NewEngine),
		fx.Provide(driver.NewHTTPServer),
		controller.Register,

		// 必须显式启动监听。ginsdk 的 HTTPServer 只是个 http.Server 的壳，
		// 自己不会去 ListenAndServe——没有这个 Invoke，服务会打满一屏
		// [Fx] RUNNING 然后安静地不监听任何端口。
		fx.Invoke(startHTTPServer),
	)
}

// core 各入口共用的装配；HTTP 监听由 Init 另加。
func core(conf *config.Config) fx.Option {
	return fx.Options(
		fx.Supply(conf),

		fx.Provide(driver.NewProvider),

		// TransProvider 同时满足两个接口，按接口分别暴露：
		// 仓储要 sqlsdk.Provider（UseDB 是事务感知的，事务里自动用同一个连接），
		// 应用层和 NewKnowledgeDocRepo 都要 transaction.Manager
		//（Save 的「锁行 + upsert」必须在一个事务里，见 A4.4）
		fx.Provide(func(p *sqlsdk.TransProvider) sqlsdk.Provider { return p }),
		fx.Provide(func(p *sqlsdk.TransProvider) transaction.Manager { return p }),

		fx.Provide(driver.NewESClient),

		// 配置 → 领域调参。
		// 在这里转一次手，application 层就不必 import infrastructure/config（§3）
		fx.Provide(func(c *config.Config) search.SearchTuning {
			return search.SearchTuning{
				BM25Top:              c.Search.BM25Top,
				KNNTops:              c.Search.KNNTops,
				NumCandidates:        c.Search.NumCandidates,
				RankConstant:         c.Search.RankConstant,
				DenseWeight:          c.Search.DenseWeight,
				DefaultRetrieveCount: c.Search.DefaultRetrieveCount,
			}
		}),

		persistence.Register,
		serviceimpl.Register,
		domain.Register,
		service.Register,

		fx.Invoke(checkTrustRequestAccount),
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
// 用 fx.Invoke 而不是放在 main 里手写 if：这样入口都会过这道闸门，漏不掉。
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

// startHTTPServer 真正把端口监听起来（§9.4 同款理由：起不来的服务不该假装起来了）。
//
// Serve 内部是阻塞的 ListenAndServe，所以只能丢进 goroutine；
// 代价是「端口被占用」这类错误发生在 goroutine 里，OnStart 看不见，
// 服务会照常进入 RUNNING——正是我们刚踩过的那个坑。
// 所以 Serve 之后再回dial一次：连得上才算启动成功。
//
// 为什么不让它 panic 了事：panic 在 goroutine 里会打崩整个进程，
// 却拿不到 fx 的回滚（已经跑起来的 OnStart 不会被回滚），
// 日志里只会剩一句语焉不详的 panic。在这里返回 error 更干净。
func startHTTPServer(lc fx.Lifecycle, srv *ginsdk.HTTPServer, conf *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// 先自己 bind 一次再放开，只为拿到一句像样的错误。
			// Serve 里的 ListenAndServe 跑在 goroutine 里，端口被占时它的 panic
			// 是打崩整个进程，而不是回到这里——日志里只会剩一段栈，
			// 「8090 被谁占了」这个信息拿不到。这里先问一次，答案就清楚了。
			// 之后 Serve 再 bind 有一次极短的抢跑窗口，概率上可以忽略。
			listenAddr := conf.HTTP.Host + ":" + conf.HTTP.Port // gin 那边也是这么拼的
			probe, err := net.Listen("tcp", listenAddr)
			if err != nil {
				return fmt.Errorf("HTTP 服务无法监听 %s: %w", listenAddr, err)
			}
			probe.Close()

			go srv.Serve(ctx)

			// Host 为空表示监听 0.0.0.0，回dial 要用具体地址
			host := conf.HTTP.Host
			if host == "" {
				host = "127.0.0.1"
			}
			addr := net.JoinHostPort(host, conf.HTTP.Port)

			// 最多等 2s。这个等待只在启动期发生一次，不影响请求路径。
			deadline := time.Now().Add(2 * time.Second)
			for {
				conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
				if err == nil {
					conn.Close()
					logsdk.Info(ctx, "HTTP 服务已监听", logsdk.Any("addr", addr))
					return nil
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("HTTP 服务未能在 %s 上监听: %w", addr, err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
				}
			}
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}
