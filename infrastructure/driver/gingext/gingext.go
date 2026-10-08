package gingext

import (
	"time"

	"github.com/PycMono/FastRAG/infrastructure/config"
	mw "github.com/PycMono/FastRAG/infrastructure/middleware"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/PycMono/go-gin-sdk/middleware"
	"github.com/gin-gonic/gin"
)

// NewEngine 初始化标准 Gin 引擎
// 引入 go-gin-sdk 的中间件能力（Bizctx、CORS、Tracing、Logger）以及框架级中间件（访问日志、限流）
//
// Bizctx 会把请求头 X-Bizctx-UserID 解析进 bizctx，
// Controller 通过 bizctx.GetUserID(c.Request.Context()) 取用户标识。
func NewEngine(conf *config.Config) *gin.Engine {
	if !conf.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// 全局中间件（顺序：业务上下文 → 跨域 → 链路 → 框架日志 → 访问日志 → 限流）
	router.Use(middleware.Bizctx())
	router.Use(middleware.CORS())
	router.Use(middleware.Tracing())
	router.Use(middleware.Logger())
	router.Use(mw.AccessLog())
	router.Use(mw.RateLimit())

	// Recovery 放在最后，确保前面中间件里的 panic 也能被捕获
	router.Use(gin.Recovery())

	return router
}

// NewHTTPServer 创建 HTTP 服务器
// 使用 go-gin-sdk 的 HTTPServer 封装，支持优雅关闭和超时配置
func NewHTTPServer(router *gin.Engine, conf *config.Config) *ginsdk.HTTPServer {
	opts := &ginsdk.ServerOptions{
		Host:         conf.HTTP.Host,
		Port:         conf.HTTP.Port,
		ReadTimeout:  time.Duration(conf.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(conf.HTTP.WriteTimeout) * time.Second,
	}

	return ginsdk.NewHTTPServer(router, opts)
}
