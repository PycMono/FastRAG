package mysql

import (
	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
)

// NewProvider 初始化 MySQL Provider（go-mysql-sdk）
func NewProvider(conf *config.Config) (sqlsdk.Provider, error) {
	opts := &sqlsdk.Options{
		Host:          conf.MySQL.Host,
		Port:          conf.MySQL.Port,
		Database:      conf.MySQL.Database,
		User:          conf.MySQL.User,
		Password:      conf.MySQL.Password,
		MaxOpen:       conf.MySQL.MaxOpen,
		MaxIdle:       conf.MySQL.MaxIdle,
		Lifetime:      conf.MySQL.ConnLifetime,
		Timeout:       conf.MySQL.ConnTimeout,
		LogLevel:      conf.MySQL.LogLevel,
		SlowThreshold: conf.MySQL.SlowThreshold,
	}

	provider := sqlsdk.NewTransProvider(opts)
	return provider, nil
}
