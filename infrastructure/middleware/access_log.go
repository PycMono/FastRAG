package middleware

import (
	"time"

	logsdk "github.com/PycMono/go-logger-sdk"
	"github.com/gin-gonic/gin"
)

// AccessLog 访问日志中间件
// 记录 method / path / status / latency_ms / client_ip
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		logsdk.Info(c.Request.Context(), "access",
			logsdk.Any("method", c.Request.Method),
			logsdk.Any("path", c.Request.URL.Path),
			logsdk.Any("status", c.Writer.Status()),
			logsdk.Any("latency_ms", time.Since(start).Milliseconds()),
			logsdk.Any("client_ip", c.ClientIP()),
		)
	}
}
