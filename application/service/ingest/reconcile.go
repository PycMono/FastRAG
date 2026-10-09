package ingest

import (
	"context"
)

// ReconcileResult 单个知识库的对账结果。
type ReconcileResult struct {
	KBNo             string `json:"kb_no"`
	DocCountBefore   int64  `json:"doc_count_before"`
	DocCountAfter    int64  `json:"doc_count_after"`
	ChunkCountBefore int64  `json:"chunk_count_before"`
	ChunkCountAfter  int64  `json:"chunk_count_after"`
}

// Reconcile 重算某个知识库的 doc_count / chunk_count 并回写。
//
// 计数是用差值维护的（§5.1），删除与异常会让它慢慢漂移——漂移只影响
// 列表页那几个数字，不影响检索正确性，所以做成离线任务而不是实时强一致。
//
// **只信 MySQL**，不去 ES 数切片：ES 是弱一致的那一侧（§5.2），
// 拿它当权威会把孤儿切片算进真实数据里。
func (s *Service) Reconcile(ctx context.Context, kbNo, account string) (*ReconcileResult, error) {
	kb, err := s.kbRepo.LoadByNo(ctx, kbNo, account)
	if err != nil {
		return nil, err
	}

	docCount, err := s.docRepo.CountByKBID(ctx, kb.ID)
	if err != nil {
		return nil, err
	}
	chunkCount, err := s.docRepo.SumChunkCountByKBID(ctx, kb.ID)
	if err != nil {
		return nil, err
	}

	// 用绝对值回写而不是补差值：对账的意义就是「以真实数据为准」，
	// 再走一次差值只是把漂移原样带过去
	if err := s.kbRepo.SetCounts(ctx, kb.ID, docCount, chunkCount); err != nil {
		return nil, err
	}

	return &ReconcileResult{
		KBNo:           kb.No,
		DocCountBefore: kb.DocCount, DocCountAfter: docCount,
		ChunkCountBefore: kb.ChunkCount, ChunkCountAfter: chunkCount,
	}, nil
}
