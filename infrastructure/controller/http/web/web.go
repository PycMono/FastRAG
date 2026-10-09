package web

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// indexHTML 演示页。整页（样式 + 脚本）就这一个文件，没有构建步骤、没有外部依赖——
// 它要能在「clone 下来、起服务、打开浏览器」之后立刻用，这才是它存在的意义。
// 引 CDN 就得有网，用框架就得有 node_modules，两者都会让这个页面在任何一台
// 只能连内网的机器上直接白屏。
//
//go:embed index.html
var indexHTML []byte

// Controller 演示页控制器。
//
// 它不属于业务链路，是给「想手工试一把」的人用的：上传 md、点检索、看切片。
// 真正调 API 的是 application 层之外的调用方，这个页面只是其中最简单的一个。
type Controller struct{}

func NewController() *Controller { return &Controller{} }

// Index GET /
//
// 用 c.Data 而不是 ginsdk.Send：这里返回的是 HTML 而不是 JSON 信封，
// 拿统一响应包装它，浏览器只会把 {"code":0,...} 渲染成一串文本。
// （这也是健康探针之外唯一一个不走 ginsdk.Send 的地方。）
func (ctl *Controller) Index(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
}
