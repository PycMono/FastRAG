package controller

import (
	"github.com/PycMono/FastRAG/infrastructure/controller/http"
	"go.uber.org/fx"
)

// Register 注册所有控制器
var Register = fx.Options(
	http.Register,
)
