package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/domain/service"
)

// stubKBRepo 只实现 FindByNo，其余方法靠内嵌的 nil 接口兜着——
// 一旦被调到就 panic，正好说明用例走到了不该走的地方。
type stubKBRepo struct {
	repository.IKnowledgeRepo
	kb *entity.Knowledge
}

func (s stubKBRepo) FindByNo(ctx context.Context, no, tenant string) (*entity.Knowledge, error) {
	return s.kb, nil
}

// errEmbeddingRegistry 任何名字都取不到，模拟调用方传了个不存在的 model。
type errEmbeddingRegistry struct{ err error }

func (r errEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return nil, r.err }

// 名字写错必须**在写任何东西之前**就返回：合并落库段之后，产 chunks 在取实现之前
// （见 ingest.go 的 ingestDoc），所以「先切片」是允许的——但一次写都不该发生：
// docRepo / store / tm 都是 nil，谁被调到谁就 panic。
func TestIngest_UnknownModel_FailsBeforeAnyWrite(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.Knowledge{}},
		nil, // docRepo：不该被调到
		nil, // store：不该被调到
		errEmbeddingRegistry{err: errors.New(`没有名为 "nope" 的 embedding 模型。可用：[bge-m3]`)},
		stubParserRegistry{}, // parsers：本用例走不到解析，给个空壳
		nil,                  // splitter
		nil,                  // idGen
		nil,                  // tm
	)

	_, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
		Tenant:  "demo",
		KBNo:    "demo-kb",
		DocName: "a.md",
		Format:  "text",
		Content: "正文",
		Model:   "nope",
	})
	if err == nil {
		t.Fatal("未知 model 必须报错，不能悄悄用默认那家")
	}
	// 错误必须来自注册表本身（文案里带 nope），而不是别的步骤顺手报的错——
	// 否则这条用例钉不住「就是「取实现」这一步失败的」。
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("应是取实现报的错（提到 nope），得到 %v", err)
	}
}

// 删除失败（文档不存在等）：整笔回滚——不扣 KB 的冗余计数，也不清 ES。
//
// 反过来（吞掉错误继续往下走）会让 KB 的 doc_count / chunk_count 永久偏小，
// 而调用方拿到的还是「删除成功」，账再也对不上。
func TestDeleteDoc_DeleteFails_NoDeltaNoESCleanup(t *testing.T) {
	kb := &entity.Knowledge{ID: 7, No: "kb-1", Tenant: "demo"}
	docRepo := &stubDocRepo{delErr: errors.New("文档不存在: gone.md")}
	store := &stubStore{}
	kbRepo := &stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}}
	svc := NewService(
		kbRepo, docRepo, store,
		okEmbeddingRegistry{e: stubEmbedding{dim: 4}},
		stubParserRegistry{parser: stubParser{name: "p"}},
		service.NewSplitter(), &stubIDService{}, &stubTm{},
	)

	_, err := svc.DeleteDoc(context.Background(), &dto.DocDeleteDTO{
		Tenant: "demo", KBNo: "kb-1", DocName: "gone.md",
	})
	if err == nil {
		t.Errorf("删除失败必须报错，不能当成功返回")
	}
	if len(store.calls) != 0 {
		t.Errorf("删除没成功就不该清 ES，得到 %v", store.calls)
	}
	if kbRepo.chunkDelta != 0 || kbRepo.docDelta != 0 {
		t.Errorf("删除没成功就不该动 KB 计数，得到 chunkDelta=%d docDelta=%d",
			kbRepo.chunkDelta, kbRepo.docDelta)
	}
}
