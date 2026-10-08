package http

import (
	"github.com/PycMono/FastRAG/infrastructure/controller/http/health"
	"github.com/PycMono/FastRAG/infrastructure/controller/http/knowledge"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

const v1Prefix = "/api/v1"

// Register 注册所有 HTTP 控制器到 FX 容器
var Register = fx.Options(
	fx.Provide(health.NewController),
	fx.Provide(knowledge.NewController),
	fx.Invoke(RegisterRoutes),
)

// RouteDeps 路由注册所需依赖（FX 自动按类型注入）
type RouteDeps struct {
	fx.In
	Router       *gin.Engine
	HealthCtl    *health.Controller
	KnowledgeCtl *knowledge.Controller
}

// RegisterRoutes 统一入口
// 页面/探针路由注册在顶层 router，所有 JSON API 路由注册在 api Group 下
func RegisterRoutes(d RouteDeps) {
	// 健康检查（顶层，非 /api 前缀）
	registerHealthRoutes(d.Router, d.HealthCtl)

	// API 路由（必须挂在 api Group 下）
	api := d.Router.Group(v1Prefix)
	{
		registerKnowledgeRoutes(api, d.KnowledgeCtl)
	}
}

func registerHealthRoutes(r *gin.Engine, healthCtl *health.Controller) {
	r.GET("/health", healthCtl.Health)
	r.GET("/ready", healthCtl.Ready)
}

func registerKnowledgeRoutes(api *gin.RouterGroup, ctl *knowledge.Controller) {
	g := api.Group("/knowledge-bases")
	{
		g.POST("", ctl.Create)
		g.GET("", ctl.List)
		g.GET("/:id", ctl.Get)
		g.PUT("/:id", ctl.Update)
		g.DELETE("/:id", ctl.Delete)

		// 检索扩展点（当前返回 ErrKnowledgeRetrievalNotImpl）
		g.POST("/:id/search", ctl.Search)
	}
}
