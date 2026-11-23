package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PycMono/FastRAG/infrastructure/observability"
	"github.com/gin-gonic/gin"
)

type Controller struct {
	config  *ConfigStore
	logs    *observability.Store
	token   string
	started time.Time
}

func NewController(logs *observability.Store) *Controller {
	return &Controller{config: NewConfigStore("config.json"), logs: logs, token: os.Getenv("FASTRAG_ADMIN_TOKEN"), started: time.Now()}
}
func reply(c *gin.Context, status int, data any, message string) {
	code := 0
	if status >= 400 {
		code = status
	}
	c.JSON(status, gin.H{"code": code, "msg": message, "data": data})
}
func (ctl *Controller) Auth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if len(ctl.token) < 24 {
		reply(c, 503, nil, "管理功能未开启：请设置至少 24 位 FASTRAG_ADMIN_TOKEN 后重启服务")
		c.Abort()
		return
	}
	supplied := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	expectedHash := sha256.Sum256([]byte(ctl.token))
	actualHash := sha256.Sum256([]byte(supplied))
	if subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) != 1 {
		reply(c, 401, nil, "管理令牌不正确或未提供")
		c.Abort()
		return
	}
	c.Next()
}
func (ctl *Controller) Status(c *gin.Context) {
	reply(c, 200, gin.H{"uptime_seconds": int(time.Since(ctl.started).Seconds()), "log_scope": "结构化应用日志（含请求访问、业务错误和生命周期）；不包含 Docker、数据库或任意 stdout/stderr 输出", "log_capacity": 2000, "log_disk_retention": "按北京时间自然月分文件，仅保留当月；跨月自动清理，页面查询最近 2000 条", "config_effect": "保存后重启生效"}, "")
}
func (ctl *Controller) ReadConfig(c *gin.Context) {
	snapshot, err := ctl.config.Read()
	if err != nil {
		reply(c, 500, nil, err.Error())
		return
	}
	reply(c, 200, snapshot, "")
}
func (ctl *Controller) SaveConfig(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		Version string         `json:"version"`
		Config  map[string]any `json:"config"`
	}
	if c.ShouldBindJSON(&body) != nil || body.Config == nil {
		reply(c, 400, nil, "请输入有效配置，最大 1 MiB")
		return
	}
	if err := ctl.config.Save(body.Version, body.Config); err != nil {
		status := 400
		if errors.Is(err, ErrConflict) {
			status = 409
		}
		reply(c, status, nil, err.Error())
		return
	}
	// Include newly entered secrets in log redaction immediately, before restart.
	if data, err := os.ReadFile("config.json"); err == nil {
		var config map[string]any
		if json.Unmarshal(data, &config) == nil {
			ctl.logs.AddSecrets(observability.SecretsFrom(config))
		}
	}
	ctl.logs.Add("info", "管理配置已保存，等待人工重启生效", nil)
	snapshot, err := ctl.config.Read()
	if err != nil {
		reply(c, 500, nil, "已保存，但读取新版本失败，请重新加载")
		return
	}
	reply(c, 200, snapshot, "")
}
func (ctl *Controller) Logs(c *gin.Context) {
	level := c.Query("level")
	if level != "" && level != "info" && level != "warn" && level != "error" && level != "debug" {
		reply(c, 400, nil, "日志级别不正确")
		return
	}
	for _, key := range []string{"since", "until"} {
		if value := c.Query(key); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				reply(c, 400, nil, "时间格式不正确")
				return
			}
		}
	}
	page, _ := strconv.Atoi(c.Query("page"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	if len(c.Query("q")) > 500 {
		reply(c, 400, nil, "搜索词过长")
		return
	}
	reply(c, 200, ctl.logs.Query(observability.Filter{Level: level, Query: c.Query("q"), Since: c.Query("since"), Until: c.Query("until"), Page: page, Limit: limit}), "")
}
