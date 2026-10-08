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
