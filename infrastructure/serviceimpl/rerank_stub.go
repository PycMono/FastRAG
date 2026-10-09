package serviceimpl

import (
	"context"

	"github.com/PycMono/FastRAG/domain/interfaces"
)

// RerankStub 重排占位实现：原样返回，不改顺序。
//
// 本期不做重排（P2），但接口先留好：接上真正的 rerank 服务时，
// 只需要换掉 serviceimpl.Register 里的这一行，应用层一行都不用动。
type RerankStub struct{}

func NewRerankStub() interfaces.IRerankService { return &RerankStub{} }

func (RerankStub) Rerank(
	_ context.Context, _ string, cands []interfaces.RerankCandidate, _ int,
) ([]int, error) {
	out := make([]int, len(cands))
	for i := range out {
		out[i] = i
	}
	return out, nil
}
