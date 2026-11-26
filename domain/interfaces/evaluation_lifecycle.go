package interfaces

import "context"

// IEvalDocumentLifecycle 处理文档派生评测数据，不参与导入事务。
type IEvalDocumentLifecycle interface {
	Imported(context.Context, string, string, string, string, string) error
	Removed(context.Context, string, string, string) error
}
