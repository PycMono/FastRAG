package domain

import (
	"github.com/PycMono/FastRAG/domain/service"
	"go.uber.org/fx"
)

// Register 注册所有领域层服务
var Register = fx.Options(
	service.Register,
)
