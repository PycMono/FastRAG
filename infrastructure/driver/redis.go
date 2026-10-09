package driver

import (
	"context"
	"fmt"

	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/go-cache-sdk/redis/connect"
	goredis "github.com/redis/go-redis/v9"
)

// NewClient 初始化 Redis 客户端（go-cache-sdk）
// redis.addr 为空表示不启用 Redis，此时返回 nil 客户端而非报错，
// 使骨架在没有 Redis 的环境下也能正常启动。
func NewRedisClient(conf *config.Config) (goredis.UniversalClient, error) {
	if len(conf.Redis.Addr) == 0 || conf.Redis.Addr[0] == "" {
		return nil, nil
	}

	rConf := &connect.Config{
		AppName:    conf.App,
		ClientName: "cache",
		Addr:       conf.Redis.Addr,
		Password:   conf.Redis.Password,
		DB:         conf.Redis.DB,
		PoolSize:   conf.Redis.PoolSize,
	}

	client, err := connect.InitClient(context.Background(), rConf)
	if err != nil {
		return nil, fmt.Errorf("init redis client failed: %w", err)
	}
	return client, nil
}
