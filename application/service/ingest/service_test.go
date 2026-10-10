package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// stubKBRepo 只实现 FindByNo，其余方法靠内嵌的 nil 接口兜着——
// 一旦被调到就 panic，正好说明用例走到了不该走的地方。
type stubKBRepo struct {
	repository.IKnowledgeBaseRepo
	kb *entity.KnowledgeBase
}

func (s stubKBRepo) FindByNo(ctx context.Context, no, account string) (*entity.KnowledgeBase, error) {
	return s.kb, nil
}

// errEmbeddingRegistry 任何名字都取不到，模拟调用方传了个不存在的 model。
type errEmbeddingRegistry struct{ err error }

func (r errEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return nil, r.err }
func (r errEmbeddingRegistry) Names() []string                                { return []string{"bge-m3"} }

// 名字写错必须**在写任何东西之前**就返回：不能先切片、先算向量、先写 ES，
// 白跑一大圈才告诉调用方"这个名字不存在"。
func TestIngest_UnknownModel_FailsBeforeAnyWrite(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, // docRepo：不该被调到
		nil, // store：不该被调到
		errEmbeddingRegistry{err: errors.New(`没有名为 "nope" 的 embedding 模型。可用：[bge-m3]`)},
		nil, // splitter
		nil, // idGen
		nil, // tm
	)

	_, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
		Account: "demo",
		KBNo:    "demo-kb",
		DocName: "a.md",
		Format:  "text",
		Content: "正文",
		Model:   "nope",
	})
	if err == nil {
		t.Fatal("未知 model 必须报错，不能悄悄用默认那家")
	}
}
