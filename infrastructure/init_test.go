package infrastructure_test

import (
	"sort"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// TestInit_BuildsGraphAndRegistersRoutes 是 T11 唯一的运行时验证。
//
// 为什么必须有它：`go build` / `go vet` 只能证明代码能编译，而 fx 的类型键
// （接口 vs 具体类型）、Provide/Decorate 冲突、循环依赖都是**运行时**失败，
// 编译期一律看不出来。若不真正跑一次 fx.New，DI 装配错误只能等到有人手动启动
// 服务时才发现——而本任务的全部价值恰恰是「依赖图装得起来、路由挂得上去」。
func TestInit_BuildsGraphAndRegistersRoutes(t *testing.T) {
	conf := &config.Config{
		App:   "fastrag-test",
		Debug: true,
		HTTP:  config.HTTPConfig{Host: "", Port: "0"},
		MySQL: config.MySQLConfig{Host: "127.0.0.1", Port: 3306, Database: "fastrag", User: "root", Password: "x"},
		// Redis 故意留空：redis.NewClient 在 addr 为空时返回 (nil, nil)，
		// fx 允许这个 nil 值进入依赖图（已实测）。
		SnowflakeNodeID: 1,

		// 地址随便给一个：NewClient 不发探活请求（A4.2 的 ⚠️），
		// 所以这里不需要真有一个 ES 在跑。
		Elasticsearch: config.ESConfig{Addrs: []string{"http://127.0.0.1:9200"}},

		// 不做鉴权的显式开关（§6.1）。不置 true 时 core 里的闸门会直接拒绝启动，
		// 装配测试也得过这一道——这正是把闸门做成 fx.Invoke 而不是写在 main 里的意义。
		Security: config.SecurityConfig{TrustRequestAccount: true},
	}

	var got []string
	app := fx.New(
		fx.NopLogger,
		infrastructure.Init(conf),
		// 顶掉 TransProvider 的构造函数，从而无需数据库即可装配整图。
		//
		// 为什么顶的是 *sqlsdk.TransProvider 而不是 sqlsdk.Provider：
		// dig 是按类型取值的，仓储要 sqlsdk.Provider、Save 的事务要
		// transaction.Manager，两个都由同一个 *TransProvider 派生。
		// 只替换下游接口的话，dig 仍会去构造上游的 TransProvider——
		// 而 mysql.NewProvider 构造时就真连库，连不上直接 panic。
		//
		// 零值 TransProvider 的 UseDB 会空指针，但本测试不执行任何数据库操作：
		// ensureIndexOnStart 是 fx.Invoke，它的 OnStart 只有在 app.Start() 时才跑。
		fx.Decorate(func() *sqlsdk.TransProvider { return &sqlsdk.TransProvider{} }),
		fx.Invoke(func(engine *gin.Engine) {
			for _, r := range engine.Routes() {
				got = append(got, r.Method+" "+r.Path)
			}
		}),
	)

	// fx.New 只构建依赖图并联执行 Invoke；不调用 Start，
	// 因此不会触发 AutoMigrate，也不会监听端口。
	if err := app.Err(); err != nil {
		t.Fatalf("fx 依赖图装配失败: %v", err)
	}

	// 路由刻意很少：只有「写文档」「删文档」「检索」三件事（§1.2）。
	// 知识库 CRUD 已移出本服务范围，这里多出来的任何一条都该被质疑。
	//
	// 两个例外，都不是业务接口：/health 与 /ready 是 K8s 探针（固定路径），
	// / 是内嵌的演示页（给人看的 HTML）。它们同样在顶层，不带 /api 前缀。
	want := []string{
		"GET /",
		"GET /health",
		"GET /ready",
		"POST /api/v1/docs",
		"POST /api/v1/docs/batch",
		"POST /api/v1/docs/delete",
		"POST /api/v1/search",
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("注册路由数 = %d, want %d\n实际: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("路由集合不匹配\n实际: %v\n期望: %v", got, want)
		}
	}
}
