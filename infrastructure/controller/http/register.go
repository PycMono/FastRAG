package http

import (
	docctl "github.com/PycMono/FastRAG/infrastructure/controller/http/doc"
	searchctl "github.com/PycMono/FastRAG/infrastructure/controller/http/search"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

const v1Prefix = "/api/v1"

// Register 注册所有 HTTP 控制器到 FX 容器
var Register = fx.Options(
	fx.Provide(docctl.NewController),
	fx.Provide(searchctl.NewController),
	fx.Invoke(RegisterRoutes),
)

// RouteDeps 路由注册所需依赖（FX 自动按类型注入）
type RouteDeps struct {
	fx.In
	Router    *gin.Engine
	DocCtl    *docctl.Controller
	SearchCtl *searchctl.Controller
}

// RegisterRoutes 统一入口。
//
// 路由刻意做得很少：只有「写文档」「删文档」「检索」三件事（§1.2）。
// 知识库的 CRUD、发布、版本全部不在本服务范围内。
func RegisterRoutes(d RouteDeps) {
	api := d.Router.Group(v1Prefix)
	{
		docs := api.Group("/docs")
		{
			docs.POST("", d.DocCtl.Ingest)
			docs.POST("/batch", d.DocCtl.BatchIngest)
			docs.POST("/delete", d.DocCtl.Delete)
		}

		api.POST("/search", d.SearchCtl.Search)
	}
}
