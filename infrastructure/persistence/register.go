package persistence

import (
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	knowledgepersistence "github.com/PycMono/FastRAG/infrastructure/persistence/knowledge"
	"go.uber.org/fx"
)

// Register 注册所有持久化实现。
//
// 三个仓储都直接返回接口类型，让 fx 按接口装配——
// 应用层拿到的永远是 IVectorStore / IKnowledgeDocRepo，不是具体实现。
//
// 注意 NewKnowledgeDocRepo 有两个参数：sqlsdk.Provider 和 transaction.Manager。
// 后者是 Save 用的（A4.4）——「锁行读旧值、upsert」两步必须在一个事务里，
// 否则两个并发导入会各自读到一个已经被对方改过的 chunk_count，
// 拿去算 KB 计数差值时就会算错。
// 这两个接口由 A4.16 的 TransProvider 一份实例同时 provide，fx 自动装配。
var Register = fx.Options(
	fx.Provide(knowledgepersistence.NewKnowledgeBaseRepo),
	fx.Provide(knowledgepersistence.NewKnowledgeDocRepo),
	fx.Provide(knowledgepersistence.NewESVectorStore),
)

// 编译期断言：实现必须满足端口。
// 放在装配处而不是实现文件里，是为了让「谁该满足哪个接口」一眼可见。
var (
	_ knowledgerepo.IKnowledgeBaseRepo = (*knowledgepersistence.KnowledgeBaseRepo)(nil)
	_ knowledgerepo.IKnowledgeDocRepo  = (*knowledgepersistence.KnowledgeDocRepo)(nil)
	_ knowledgerepo.IVectorStore       = (*knowledgepersistence.ESVectorStore)(nil)
)
