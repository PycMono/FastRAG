package http

import (
	docctl "github.com/PycMono/FastRAG/infrastructure/controller/http/doc"
	"github.com/PycMono/FastRAG/infrastructure/controller/http/health"
	searchctl "github.com/PycMono/FastRAG/infrastructure/controller/http/search"
	webctl "github.com/PycMono/FastRAG/infrastructure/controller/http/web"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

const v1Prefix = "/api/v1"

// Register 注册所有 HTTP 控制器到 FX 容器
var Register = fx.Options(
	fx.Provide(health.NewController),
	fx.Provide(docctl.NewController),
	fx.Provide(docctl.NewReadController),
	fx.Provide(searchctl.NewController),
	fx.Provide(webctl.NewController),
	fx.Invoke(RegisterRoutes),
)

// RouteDeps 路由注册所需依赖（FX 自动按类型注入）
type RouteDeps struct {
	fx.In
	Router     *gin.Engine
	HealthCtl  *health.Controller
	DocCtl     *docctl.Controller
	DocReadCtl *docctl.ReadController
	SearchCtl  *searchctl.Controller
	WebCtl     *webctl.Controller
}

// RegisterRoutes 统一入口。
//
// 业务路由刻意做得很少：只有「写文档」「解析导入」「删文档」「检索」「看文档」五件事（§1.2）。
// 知识库的 CRUD、发布、版本全部不在本服务范围内。
//
// 探针路由（/health、/ready）注册在顶层、不带 /api 前缀——
// K8s 的 livenessProbe / readinessProbe 按固定路径打，前缀变了探针就瞎了。
//
// 演示页挂在根路径，同样是顶层：它是给人看的 HTML，不是 API，
// 塞进 /api/v1 只会让「哪些路径是接口」这件事变得含糊。
func RegisterRoutes(d RouteDeps) {
	registerHealthRoutes(d.Router, d.HealthCtl)
	d.Router.GET("/", d.WebCtl.Index)

	api := d.Router.Group(v1Prefix)
	{
		docs := api.Group("/docs")
		{
			docs.POST("", d.DocCtl.Ingest)
			docs.POST("/batch", d.DocCtl.BatchIngest)
			docs.POST("/delete", d.DocCtl.Delete)
			docs.POST("/parse", d.DocCtl.Parse)

			// 只读三条：GET + query 参数（本仓第一次出现 GET 业务接口）。
			// 与写共用同一个 Group——读与写是同一个资源
			docs.GET("", d.DocReadCtl.List)
			docs.GET("/detail", d.DocReadCtl.Detail)
			docs.GET("/chunks", d.DocReadCtl.Chunks)
		}

		api.POST("/search", d.SearchCtl.Search)
	}
}

func registerHealthRoutes(r *gin.Engine, ctl *health.Controller) {
	r.GET("/health", ctl.Health)
	r.GET("/ready", ctl.Ready)
}
