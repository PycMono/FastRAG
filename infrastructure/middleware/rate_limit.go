package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimitEntry 单个 IP 的请求记录
type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

// RateLimiter IP 级频率限制器
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	max     int
	window  time.Duration
}

// NewRateLimiter 创建频率限制器
//   - max: 窗口内允许的最大请求数
//   - window: 计数窗口
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		max:     max,
		window:  window,
	}

	// 定期清理过期条目，避免 map 无限增长
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for key, e := range rl.entries {
				if now.After(e.resetAt) {
					delete(rl.entries, key)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return rl
}

// Allow 检查 IP 是否允许通过
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, ok := rl.entries[ip]
	if !ok || now.After(e.resetAt) {
		rl.entries[ip] = &rateLimitEntry{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	if e.count >= rl.max {
		return false
	}

	e.count++
	return true
}

// RateLimit 返回 gin 频率限制中间件
// 默认：600 请求 / 分钟
func RateLimit() gin.HandlerFunc {
	limiter := NewRateLimiter(600, time.Minute)

	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP()) {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		c.Next()
	}
}
