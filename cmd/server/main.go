package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	_ "time/tzdata" // 内嵌时区库：目标机缺 tzdata 时 DSN 的 loc=PRC 会解析失败

	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/observability"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

func main() {
	store, err := observability.NewStore("logs/service.jsonl", 2000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法创建日志存储:", err)
		os.Exit(1)
	}
	defer store.Close()
	if data, err := os.ReadFile("config.json"); err == nil {
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			store.SetSecrets(observability.SecretsFrom(cfg))
		}
	}
	store.AddSecrets([]string{os.Getenv("FASTRAG_ADMIN_TOKEN")})
	logsdk.SetLogger(observability.Logger{Logger: logsdk.NewLogrus(logsdk.Options{Module: "fastrag"}), Store: store})

	app := fx.New(infrastructure.Init(config.MustLoad()), fx.Supply(store), fx.Invoke(func(lifecycle fx.Lifecycle) {
		lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error { logsdk.Info(ctx, "FastRAG 服务已启动"); return nil }, OnStop: func(ctx context.Context) error { logsdk.Info(ctx, "FastRAG 服务正在停止"); return nil }})
	}))
	app.Run()
}
