package service

import "go.uber.org/fx"

// Register 注册所有领域层服务。
//
// 切片器虽然是纯逻辑，但注册进来更好：应用层直接要一个 *Splitter，
// 而不是到处 service.NewSplitter()——将来它一旦需要注入字典/配置，
// 只需改这一处。
var Register = fx.Options(
	fx.Provide(NewSplitter),
)
