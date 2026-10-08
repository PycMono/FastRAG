package service

import (
	"go.uber.org/fx"
)

// Register 注册所有领域层服务
var Register = fx.Options(
// 领域服务为纯逻辑，无需 fx.Provide（由应用服务直接调用）
// 若未来领域服务需要依赖注入，在此注册
)
