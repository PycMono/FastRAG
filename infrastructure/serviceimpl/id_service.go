package serviceimpl

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

// IDService ID 服务实现
type IDService struct {
	node *snowflake.Node
}

// NewIDService 创建 ID 服务
//   - workerID: 雪花算法工作节点 ID，范围 0-1023，多实例时必须全局唯一
func NewIDService(workerID int64) (*IDService, error) {
	node, err := snowflake.NewNode(workerID)
	if err != nil {
		return nil, fmt.Errorf("create snowflake node: %w", err)
	}
	return &IDService{node: node}, nil
}

// NextID 生成 string 类型雪花 ID
func (svc *IDService) NextID() string {
	return svc.node.Generate().String()
}

// NextIntID 生成 int64 类型雪花 ID
func (svc *IDService) NextIntID() int64 {
	return svc.node.Generate().Int64()
}
