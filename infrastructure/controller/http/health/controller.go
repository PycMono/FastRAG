package health

import (
	"context"
	"net/http"
	"time"

	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
	"github.com/gin-gonic/gin"
)

// Controller 健康检查控制器
type Controller struct {
	conf     *config.Config
	provider sqlsdk.Provider
	es       *elasticsearch.Client
}

// NewController 创建健康检查控制器。
//
// 没有 Redis：限流本期不做（§10.3），Redis 已从 fx 图里摘掉
// （A0.2「保留但不上图」）。依赖没了，就绪探针里也就没有再报它状态的道理——
// 探一个已经不参与请求链路的东西，只会让人以为它在起作用。
func NewController(conf *config.Config, provider sqlsdk.Provider, es *elasticsearch.Client) *Controller {
	return &Controller{conf: conf, provider: provider, es: es}
}

// Health 存活探针 —— 服务是否正在运行
// 注意：本方法不走统一响应封装（ginsdk.Send），K8s 探针需要固定格式
func (ctl *Controller) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "up",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪探针 —— 依赖是否健康
// 注意：本方法不走统一响应封装，K8s 探针需要固定格式
//
// 检 MySQL 和 ES 两个。**ES 必须检**：NewClient 不发探活请求（A4.2），
// ES 没起来服务照样能启动，直到第一次导入才报错。
// 也就是说，ES 挂掉这件事只有这里能提前发现。
func (ctl *Controller) Ready(c *gin.Context) {
	checks := gin.H{}
	allHealthy := true

	// MySQL 检查
	if ctl.conf.MySQL.Host != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		sqlDB, err := ctl.provider.UseDB(ctx).DB()
		if err != nil || sqlDB.PingContext(ctx) != nil {
			checks["mysql"] = "unhealthy"
			allHealthy = false
		} else {
			checks["mysql"] = "ok"
		}
	}

	// ES 检查
	if len(ctl.conf.ES.Addrs) == 0 || ctl.conf.ES.Addrs[0] == "" {
		checks["elasticsearch"] = "not_configured"
		allHealthy = false
	} else if ctl.es == nil {
		checks["elasticsearch"] = "not_initialized"
		allHealthy = false
	} else {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if _, err := ctl.es.Ping(ctl.es.Ping.WithContext(ctx)); err != nil {
			checks["elasticsearch"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			checks["elasticsearch"] = "ok"
		}
	}

	if allHealthy {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "checks": checks})
}
