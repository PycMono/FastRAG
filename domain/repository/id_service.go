package repository

// IIDService ID 生成服务接口
type IIDService interface {
	NextID() string
	NextIntID() int64
}
