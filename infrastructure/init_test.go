package infrastructure_test

import (
	"context"
	"sort"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// stubProvider 占位 MySQL Provider。
// 本测试验证的是依赖图能否装配、路由能否注册，不是数据库连通性。
type stubProvider struct{}

func (stubProvider) UseDB(ctx context.Context) *gorm.DB {
	panic("stubProvider.UseDB 不应被调用：冒烟测试不执行任何数据库操作")
}

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
	}

	var got []string
	app := fx.New(
		fx.NopLogger,
		infrastructure.Init(conf),
		// 用桩替换真实 MySQL provider，从而无需数据库即可装配整图
		fx.Decorate(func() sqlsdk.Provider { return stubProvider{} }),
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

	want := []string{
		"DELETE /api/v1/knowledge-bases/:id",
		"GET /api/v1/knowledge-bases",
		"GET /api/v1/knowledge-bases/:id",
		"GET /health",
		"GET /ready",
		"POST /api/v1/knowledge-bases",
		"POST /api/v1/knowledge-bases/:id/search",
		"PUT /api/v1/knowledge-bases/:id",
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
