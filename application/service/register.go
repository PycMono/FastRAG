package service

import (
	"github.com/PycMono/FastRAG/application/service/knowledge"
	"go.uber.org/fx"
)

// Register 注册所有应用层服务
var Register = fx.Options(
	fx.Provide(knowledge.NewService),
)
