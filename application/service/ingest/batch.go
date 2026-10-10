// 本文件是批量导入：受限并发 + 单条错误隔离，逐条复用单文档 Ingest。
// 服务主体见 service.go，单文档导入见 ingest.go。

package ingest

import (
	"context"
	"sort"
	"sync"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/common/utils"
	"github.com/PycMono/FastRAG/common/vo"
)

// batchConcurrency 批量导入的并发度。
//
// 压得很低是刻意的：真正的瓶颈在 embedding 服务，并发开大只会把下游打挂，
// 结果整体更慢。
const batchConcurrency = 4

// BatchResult 批量导入结果。
type BatchResult struct {
	Total   int               `json:"total"`
	Success int               `json:"success"`
	Failed  int               `json:"failed"`
	Items   []*vo.DocIngestVO `json:"items"`
	Errors  []*BatchError     `json:"errors"`
}

// BatchError 单条失败明细。
type BatchError struct {
	DocName string `json:"doc_name"`
	Message string `json:"message"`
}

// docKey 文档的同一性，与 MySQL 的唯一键 (kb_id, name) 对齐。
//
// 用 kb_no 而不是 kb_id：这一层还没查库，拿不到自增主键。
// kb_no 到 kb_id 是一对一，所以在这里做去重等价。
type docKey struct {
	Account string
	KBNo    string
	DocName string
}

// docKeyOf 取一条导入请求的同一性键。
func docKeyOf(in *dto.DocIngestDTO) docKey {
	return docKey{Account: in.Account, KBNo: in.KBNo, DocName: in.DocName}
}

// BatchIngest 批量导入：受限并发 + 单条错误隔离。
//
// 同步执行，受 HTTP 超时约束；超大文档由上游分批调用。
// 不用事务包整批：一条失败不该把已经灌好的几十条一起回滚。
func (s *Service) BatchIngest(ctx context.Context, items []*dto.DocIngestDTO) *BatchResult {
	res := &BatchResult{Total: len(items)}

	// 批次内同名的先挑出来标记失败并排除。整批不因它们返回 400——
	// 控制器已约定「恒返回 200 + 明细」，合法的那几十条照常导。
	//
	// 为什么是拒绝而不是「后一条覆盖前一条」：同名两条该留哪一份是业务决策，
	// 服务端替调用方定夺，只会让「我明明传了正确的那份」变成无从解释。
	// 也不能靠并发写去碰运气——同一个 doc_id 上两路 DeleteExcept 会互相删掉
	// 对方刚写的切片，最终只剩两边集合的交集，内容被截断。
	//
	// 必须**同步**跑完再起工作池：放进 goroutine 里就回到竞态了。
	dup := utils.DuplicatedKeys(items, docKeyOf)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, batchConcurrency)

	for _, item := range items {
		if _, isDup := dup[docKeyOf(item)]; isDup {
			res.Failed++
			res.Errors = append(res.Errors, &BatchError{
				DocName: item.DocName,
				Message: "同一批次内 doc_name 重复，无法判定以哪一份为准，本批全部跳过",
			})
			continue
		}

		wg.Add(1)
		go func(in *dto.DocIngestDTO) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: ctx.Err().Error(),
				})
				mu.Unlock()
				return
			}

			out, err := s.Ingest(ctx, in)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: err.Error(),
				})
				return
			}
			res.Success++
			res.Items = append(res.Items, out)
		}(item)
	}
	wg.Wait()

	// 并发完成顺序不稳定，返回前定序——否则同一批请求两次返回的顺序都不一样
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].DocName < res.Items[j].DocName })
	sort.Slice(res.Errors, func(i, j int) bool { return res.Errors[i].DocName < res.Errors[j].DocName })

	return res
}
