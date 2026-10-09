package search

import (
	"sort"

	"github.com/PycMono/FastRAG/domain/repository"
)

// fusionOversample 融合前先把候选放大若干倍。
//
// 两路各取 topN，融合后靠前的位置可能凑不满 N 条；后面还有存活校验
// 会再砍一刀（§7.3）。放大一点，保证最终还能凑够 limit。
const fusionOversample = 4

// Route 一路检索结果 + 它在融合里的权重。
//
// 「一路」= 一个分组的一条检索路径（BM25 或 kNN）。分组见 §7.2：
// 不同 search_mode 的库不并集检索，所以多库查询会产生多路。
type Route struct {
	Hits   []repository.VectorHit
	Weight float64
}

// Fuse 用加权 RRF 融合多路结果（§7.2）。
//
//	score(d) = Σ_r  w_r / (k + rank_r(d))
//
// 关键在 **rank_r 是「这一路内部」的名次**，不是把所有路拼成一个大列表之后的名次。
// 拼成大列表再算名次会有排序偏置：后拼接的那一路的第一名，名次会被排到
// 前一路所有命中之后，等于凭空判它出局。多分组检索（§7.2）下这会让其中一组
// 的召回永远沉底，而结果还"看起来挺正常"。
//
// 因为每一路各自从 rank=1 起算，Σ 是对称的，**路的先后顺序不影响结果**。
//
// 权重让 dense_weight 真的起作用：w_knn = dense_weight，w_bm25 = 1 - dense_weight。
// 只发一路时（dense_weight 取 0 或 1）权重退化成常数，不影响名次。
//
// 不用 ES 原生的 rrf retriever：那是 basic license 下的付费特性，
// 会直接报 non-compliant。核心检索不能绑在付费特性上。
func Fuse(routes []Route, k, limit int) []repository.VectorHit {
	if k <= 0 {
		k = 60 // RRF 论文的默认值
	}

	type entry struct {
		hit   repository.VectorHit
		score float64
	}

	acc := make(map[string]*entry, 256)
	for _, r := range routes {
		if r.Weight <= 0 {
			continue // 权重为 0 的路不参与：贡献恒为 0，跳过省一遍遍历
		}
		for rank, h := range r.Hits {
			e, ok := acc[h.ChunkID]
			if !ok {
				e = &entry{hit: h}
				acc[h.ChunkID] = e
			}
			// rank 从 0 起，公式里的名次是 1-based
			e.score += r.Weight / float64(k+rank+1)
		}
	}

	out := make([]repository.VectorHit, 0, len(acc))
	for _, e := range acc {
		e.hit.Score = e.score
		out = append(out, e.hit)
	}

	// 同分时按 chunk_id 定序：map 遍历顺序是随机的，
	// 不 tie-break 的话同一个请求两次返回的顺序会不一样
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
