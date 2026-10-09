package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
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
	Rerank    RerankConfig    `json:"rerank"`
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

// ─── 模型注册表：embedding / rerank ────────────────────────────────────────
//
// 两块都分两层：外层是全体共用的默认值，models 里是逐家不同的部分。
// 划分规则一句话——共用的放外层大家一起持有，只有逐家不同、或者要单独调的
// 才放进 models（设计文档 §3）。
//
// 下面两个字段是刻意只在外层的：
//   - dim   —— 索引的性质（content_vec 的 dims 建好就改不了），不是某家的性质
//   - top_n —— 服务级的策略（先全量拿分、最后自己截断），任何一家单独改了都不成立
//
// 而 query_prefix 反过来只在 entry 里，它是模型自身的性质。

// 协议名。空串等价于 ProtocolOpenAI。
const (
	ProtocolOpenAI    = "openai"
	ProtocolDashScope = "dashscope"
)

// EmbeddingConfig 向量化服务的注册表。
type EmbeddingConfig struct {
	Default   string                    `json:"default"` // 请求没带 model 时用哪个；必须是 models 的一个 key
	Dim       int                       `json:"dim"`     // 索引的性质，不是某家的性质
	BatchSize int                       `json:"batch_size"`
	TimeoutMS int                       `json:"timeout_ms"`
	Models    map[string]EmbeddingEntry `json:"models"`
}

// EmbeddingEntry 一家向量化服务，只放"逐家不同"的字段。
type EmbeddingEntry struct {
	Protocol    string `json:"protocol"` // "" 或 "openai" | "dashscope"
	URL         string `json:"url"`      // 完整端点，后缀也要写
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`        // 发给上游的真名；留空 = 用 map 的 key
	BatchSize   int    `json:"batch_size"`   // 0 = 继承外层
	TimeoutMS   int    `json:"timeout_ms"`   // 0 = 继承外层
	QueryPrefix string `json:"query_prefix"` // 不写 = 不加前缀
}

// EmbeddingParams 造一个 embedding 实现所需的全部参数：外层与 entry 合并后的结果。
//
// 合并发生在构造注册表的时候（启动期一次），所以实现体里读到的 BatchSize /
// TimeoutMS 一定是有效值，不必再判 0，也不必知道"继承"这回事。
type EmbeddingParams struct {
	Name        string // models 里的 key；日志与报错用
	Protocol    string
	URL         string
	APIKey      string
	Model       string
	Dim         int
	BatchSize   int
	TimeoutMS   int
	QueryPrefix string
}

// Params 取某个 embedding 的完整参数；name 为空时用 Default。
func (c EmbeddingConfig) Params(name string) (EmbeddingParams, error) {
	if name == "" {
		name = c.Default
	}
	e, ok := c.Models[name]
	if !ok {
		return EmbeddingParams{}, unknownModel("embedding", name, c.Names())
	}

	p := EmbeddingParams{
		Name:        name,
		Protocol:    e.Protocol,
		URL:         e.URL,
		APIKey:      e.APIKey,
		Model:       e.Model,
		Dim:         c.Dim,
		BatchSize:   e.BatchSize,
		TimeoutMS:   e.TimeoutMS,
		QueryPrefix: e.QueryPrefix,
	}
	if p.Model == "" {
		p.Model = name
	}
	if p.BatchSize <= 0 {
		p.BatchSize = c.BatchSize
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = c.TimeoutMS
	}
	return p, nil
}

// Names 返回所有可用的名字，已排序——报错消息里的顺序不能每次都变。
func (c EmbeddingConfig) Names() []string { return sortedKeys(c.Models) }

// validate 校验 embedding 这块能不能用。
func (c EmbeddingConfig) validate() error {
	names := c.Names()
	if len(names) == 0 {
		return errors.New("embedding.models 为空：至少要配一家向量化服务")
	}
	if c.Default == "" {
		return fmt.Errorf("embedding.default 为空：请求不带 model 时没有兜底。可用：%v", names)
	}
	if _, ok := c.Models[c.Default]; !ok {
		return fmt.Errorf("embedding.default=%q 不在 models 里。可用：%v", c.Default, names)
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("embedding.batch_size 必须大于 0，当前 %d", c.BatchSize)
	}
	if c.TimeoutMS <= 0 {
		return fmt.Errorf("embedding.timeout_ms 必须大于 0，当前 %d", c.TimeoutMS)
	}
	for _, name := range names {
		e := c.Models[name]
		if err := validateEndpoint("embedding", name, e.Protocol, e.URL); err != nil {
			return err
		}
	}
	return nil
}

// RerankConfig 重排序服务的注册表。
//
// enabled=false 时整个实例不做重排：models 允许为空，default 也不要求，
// 任何名字都会拿到空实现（保持"没配 rerank 服务也能正常启动和检索"的语义）。
type RerankConfig struct {
	Enabled     bool                   `json:"enabled"`
	Default     string                 `json:"default"`
	BatchSize   int                    `json:"batch_size"`
	Concurrency int                    `json:"concurrency"`
	TimeoutMS   int                    `json:"timeout_ms"`
	TopN        int                    `json:"top_n"` // 0 = 全部返回，最后按 limit 截断
	Models      map[string]RerankEntry `json:"models"`
}

// RerankEntry 一家重排序服务，只放"逐家不同"的字段。
type RerankEntry struct {
	Protocol    string `json:"protocol"` // "" 或 "openai" | "dashscope"
	URL         string `json:"url"`      // 完整端点，后缀也要写
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	BatchSize   int    `json:"batch_size"`  // 0 = 继承外层
	Concurrency int    `json:"concurrency"` // 0 = 继承外层
	TimeoutMS   int    `json:"timeout_ms"`  // 0 = 继承外层
}

// RerankParams 造一个 rerank 实现所需的全部参数：外层与 entry 合并后的结果。
type RerankParams struct {
	Name        string
	Protocol    string
	URL         string
	APIKey      string
	Model       string
	BatchSize   int
	Concurrency int
	TimeoutMS   int
	TopN        int
}

// Params 取某个 rerank 的完整参数；name 为空时用 Default。
func (c RerankConfig) Params(name string) (RerankParams, error) {
	if name == "" {
		name = c.Default
	}
	e, ok := c.Models[name]
	if !ok {
		return RerankParams{}, unknownModel("rerank", name, c.Names())
	}

	p := RerankParams{
		Name:        name,
		Protocol:    e.Protocol,
		URL:         e.URL,
		APIKey:      e.APIKey,
		Model:       e.Model,
		BatchSize:   e.BatchSize,
		Concurrency: e.Concurrency,
		TimeoutMS:   e.TimeoutMS,
		TopN:        c.TopN,
	}
	if p.Model == "" {
		p.Model = name
	}
	if p.BatchSize <= 0 {
		p.BatchSize = c.BatchSize
	}
	if p.Concurrency <= 0 {
		p.Concurrency = c.Concurrency
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = c.TimeoutMS
	}
	return p, nil
}

// Names 返回所有可用的名字，已排序。
func (c RerankConfig) Names() []string { return sortedKeys(c.Models) }

// validate 校验 rerank 这块能不能用。
func (c RerankConfig) validate() error {
	if !c.Enabled {
		// 整个实例不做重排：这一块允许整块为空，也不要求 default
		return nil
	}

	names := c.Names()
	if len(names) == 0 {
		return errors.New("rerank.enabled=true 但 rerank.models 为空：至少配一家，或把 enabled 改成 false")
	}
	if c.Default == "" {
		return fmt.Errorf("rerank.default 为空：请求不带 rerank_model 时没有兜底。可用：%v", names)
	}
	if _, ok := c.Models[c.Default]; !ok {
		return fmt.Errorf("rerank.default=%q 不在 models 里。可用：%v", c.Default, names)
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("rerank.batch_size 必须大于 0，当前 %d", c.BatchSize)
	}
	if c.Concurrency <= 0 {
		return fmt.Errorf("rerank.concurrency 必须大于 0，当前 %d", c.Concurrency)
	}
	if c.TimeoutMS <= 0 {
		return fmt.Errorf("rerank.timeout_ms 必须大于 0，当前 %d", c.TimeoutMS)
	}
	for _, name := range names {
		e := c.Models[name]
		if err := validateEndpoint("rerank", name, e.Protocol, e.URL); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys 取 map 的 key 并排序。列可用名字时用，顺序必须稳定。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unknownModel 组装"名字不在注册表里"的错误，带上可用名单。
func unknownModel(kind, name string, names []string) error {
	return fmt.Errorf("没有名为 %q 的 %s 模型。可用：%v", name, kind, names)
}

// validateEndpoint 校验一家服务商的端点：url 必填且写全，protocol 只能是已知的几个。
func validateEndpoint(kind, name, protocol, url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("%s.models[%s].url 为空：必须写完整端点（含后缀）", kind, name)
	}
	switch protocol {
	case "", ProtocolOpenAI, ProtocolDashScope:
		return nil
	}
	return fmt.Errorf("%s.models[%s].protocol=%q 不认识，只能是 %q 或 %q",
		kind, name, protocol, ProtocolOpenAI, ProtocolDashScope)
}

// validate 启动期校验（设计文档 §7）。任一条件不满足就返回错误，服务起不来。
func (c *Config) validate() error {
	if err := c.Embedding.validate(); err != nil {
		return err
	}
	return c.Rerank.validate()
}

// SearchConfig 检索调参
type SearchConfig struct {
	BM25Top              int     `json:"bm25_top"`
	KNNTops              int     `json:"knn_top"`
	NumCandidates        int     `json:"num_candidates"`
	RankConstant         int     `json:"rank_constant"`
	DenseWeight          float64 `json:"dense_weight"`
	DefaultRetrieveCount int     `json:"default_retrieve_count"` // rerank 开启时默认召回池，0 表示 limit*2
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

	// 配置错了要在启动时就说清楚，不能等到第一次检索才发现（§7）
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config.json 校验失败: %w", err)
	}

	conf = c
	return c, nil
}

// Get 获取全局配置
func Get() *Config {
	return conf
}
