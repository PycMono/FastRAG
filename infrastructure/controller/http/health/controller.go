package health

import (
	"context"
	"net/http"
	"time"

	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

// Redis 未启用时 config.Redis.Addr 为空
const redisDisabled = "disabled"

// Controller 健康检查控制器
type Controller struct {
	conf     *config.Config
	provider sqlsdk.Provider
	redis    goredis.UniversalClient
}

// NewController 创建健康检查控制器
func NewController(conf *config.Config, provider sqlsdk.Provider, redisClient goredis.UniversalClient) *Controller {
	return &Controller{conf: conf, provider: provider, redis: redisClient}
}

// Health 存活探针 —— 服务是否正在运行
// 注意：本方法不走统一响应封装（gingext.Send），K8s 探针需要固定格式
func (ctl *Controller) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "up",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪探针 —— 依赖是否健康
// 注意：本方法不走统一响应封装，K8s 探针需要固定格式
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

	// Redis 检查（未启用时跳过）
	if len(ctl.conf.Redis.Addr) == 0 || ctl.conf.Redis.Addr[0] == "" {
		checks["redis"] = redisDisabled
	} else if ctl.redis == nil {
		checks["redis"] = "not_initialized"
		allHealthy = false
	} else {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := ctl.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			checks["redis"] = "ok"
		}
	}

	if allHealthy {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "checks": checks})
}
