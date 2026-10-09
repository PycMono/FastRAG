package driver

import (
	"fmt"

	"github.com/PycMono/FastRAG/infrastructure/config"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
)

// NewClient 构造 ES 客户端。
//
// 用 esapi（低层 API）而不是 v9 的 typed API：我们的查询体是手写 JSON，
// 低层 API 直接吃 io.Reader，反而比 typed API 那套泛型 option 链更直白。
func NewESClient(conf *config.Config) (*elasticsearch.Client, error) {
	if len(conf.ES.Addrs) == 0 || conf.ES.Addrs[0] == "" {
		return nil, fmt.Errorf("elasticsearch.addrs 未配置")
	}

	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: conf.ES.Addrs,
		Username:  conf.ES.Username,
		Password:  conf.ES.Password,

		MaxRetries: 3,
		// 429 也要重试：ES 写入队列满时会限流，这正是最该退避重试的场景
		RetryOnStatus: []int{502, 503, 504, 429},
	})
	if err != nil {
		return nil, fmt.Errorf("init elasticsearch client failed: %w", err)
	}
	return client, nil
}
