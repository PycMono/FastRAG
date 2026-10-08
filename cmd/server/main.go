package main

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	ginsdk "github.com/PycMono/go-gin-sdk"
	logsdk "github.com/PycMono/go-logger-sdk"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"go.uber.org/fx"
)

func main() {
	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag"}))

	conf := config.MustLoad()

	app := fx.New(
		infrastructure.Init(conf),
		fx.Invoke(func(lifecycle fx.Lifecycle, server *ginsdk.HTTPServer, provider sqlsdk.Provider) {
			lifecycle.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// AutoMigrate 数据库表
					if err := provider.UseDB(ctx).AutoMigrate(
						&knowledge.KnowledgeBase{},
					); err != nil {
						logsdk.Error(ctx, "auto migrate failed", logsdk.Err(err))
						return err
					}
					logsdk.Info(ctx, "auto migrate completed")

					logsdk.Info(ctx, "HTTP server starting", logsdk.Any("port", conf.HTTP.Port))
					go func() {
						server.Serve(ctx)
					}()
					return nil
				},
				OnStop: func(ctx context.Context) error {
					logsdk.Info(ctx, "HTTP server shutting down")
					return server.Shutdown(ctx)
				},
			})
		}),
	)

	app.Run()
}
