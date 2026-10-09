package config

import (
	"encoding/json"
	"os"
)

var conf *Config

// Config 配置结构
type Config struct {
	App             string      `json:"app"`               // 应用名称
	Debug           bool        `json:"debug"`             // 调试模式
	HTTP            HTTPConfig  `json:"http"`              // HTTP 配置
	MySQL           MySQLConfig `json:"mysql"`             // MySQL 配置
	Redis           RedisConfig `json:"redis"`             // Redis 配置
	SnowflakeNodeID int         `json:"snowflake_node_id"` // 雪花 ID 节点号 [0, 1023]

	ES        ESConfig        `json:"es"` // Elasticsearch
	Embedding EmbeddingConfig `json:"embedding"`
	Search    SearchConfig    `json:"search"`
	Security  SecurityConfig  `json:"security"`
}

// SecurityConfig 部署前提的显式确认（§6.1）。
//
// 默认零值 = false = 拒绝启动。这是刻意的：让「开箱即跑」这个动作
// 在部署时（有人看日志）失败，而不是在生产时静默地不设防。
type SecurityConfig struct {
	TrustRequestAccount bool `json:"trust_request_account"`
}

// ESConfig Elasticsearch 配置（json key 就是 "es"，字段名跟着 key 走）。
//
// 只有「怎么连 + 读写哪个索引」——**不含索引怎么建**。
// 维度 / 分词器 / 分片 / 副本 / 刷新间隔都搬去了 scripts/create-es-index.sh（§9.4）：
// 那些是建索引时的事实，运行期一次都不用，配置里留着只会让人以为改它能生效。
type ESConfig struct {
	Addrs    []string `json:"addrs"` // 必须带 scheme，如 http://127.0.0.1:9200
	Index    string   `json:"index"` // 留空用 knowledge.IndexName
	Username string   `json:"username"`
	Password string   `json:"password"`
}

// EmbeddingConfig 向量化服务配置（OpenAI 兼容协议）
type EmbeddingConfig struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	Dim         int    `json:"dim"`
	BatchSize   int    `json:"batch_size"`
	TimeoutMS   int    `json:"timeout_ms"`
	QueryPrefix string `json:"query_prefix"` // 非对称编码前缀，如 "query: "；多数模型留空
}

// SearchConfig 检索调参
type SearchConfig struct {
	BM25Top       int     `json:"bm25_top"`
	KNNTops       int     `json:"knn_top"`
	NumCandidates int     `json:"num_candidates"`
	RankConstant  int     `json:"rank_constant"`
	DenseWeight   float64 `json:"dense_weight"`
}

// HTTPConfig HTTP 配置
type HTTPConfig struct {
	Host         string `json:"host"`          // 监听主机，空字符串表示监听 0.0.0.0
	Port         string `json:"port"`          // 监听端口
	ReadTimeout  int    `json:"read_timeout"`  // 读取超时（秒）
	WriteTimeout int    `json:"write_timeout"` // 写入超时（秒）
}

// MySQLConfig MySQL 配置
type MySQLConfig struct {
	Host          string `json:"host"`           // 主机
	Port          int    `json:"port"`           // 端口
	Database      string `json:"database"`       // 数据库名
	User          string `json:"user"`           // 用户名
	Password      string `json:"password"`       // 密码
	MaxOpen       int    `json:"max_open"`       // 最大连接数
	MaxIdle       int    `json:"max_idle"`       // 最大空闲连接数
	ConnLifetime  int    `json:"conn_lifetime"`  // 连接最大生命周期（分钟）
	ConnTimeout   int    `json:"conn_timeout"`   // 连接超时（秒）
	LogLevel      int    `json:"log_level"`      // GORM 日志级别
	SlowThreshold int    `json:"slow_threshold"` // 慢查询阈值（毫秒）
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Addr     []string `json:"addr"`      // Redis 地址列表，为空表示不启用
	Password string   `json:"password"`  // 密码
	DB       int      `json:"db"`        // 数据库编号
	PoolSize int      `json:"pool_size"` // 连接池大小
}

// MustLoad 加载配置，失败则 panic
func MustLoad() *Config {
	c, err := Load()
	if err != nil {
		panic(err)
	}

	return c
}

// Load 从 config.json 加载配置
func Load() (*Config, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}

	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}

	conf = c
	return c, nil
}

// Get 获取全局配置
func Get() *Config {
	return conf
}
