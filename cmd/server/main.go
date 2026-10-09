package main

import (
	"context"

	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

func main() {
	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag"}))

	app := fx.New(infrastructure.Init(config.MustLoad()))
	app.Run()
}
