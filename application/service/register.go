package service

import (
	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/application/service/search"
	"go.uber.org/fx"
)

// Register 注册所有应用层服务。
//
// 原来的 knowledge.NewService 已去掉：KB 管理移出范围（§1.2），
// KB 只读，没有写用例。
var Register = fx.Options(
	fx.Provide(ingest.NewService),
	fx.Provide(search.NewService),
)
