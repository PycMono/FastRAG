package mysql

import (
	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
)

// NewProvider 初始化 MySQL Provider（go-mysql-sdk）。
//
// 返回具体类型 *sqlsdk.TransProvider 而不是 sqlsdk.Provider：
// TransProvider 同时满足 sqlsdk.Provider 和 transaction.Manager 两个接口，
// 装配层要按这两个接口各暴露一份（init.go 的 core）。返回接口类型的话，
// 那两个 fx.Provide 拿不到具体类型，只能报「already provided」。
func NewProvider(conf *config.Config) (*sqlsdk.TransProvider, error) {
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
