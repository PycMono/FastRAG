package value_object

// SearchTuning 检索调参。
//
// 单独抽出来是因为它来自配置，而 **application 层不能 import infrastructure/config**
// （§3 的分层铁律）。让 infrastructure 读配置、构造这个中立结构再注入，
// 应用层就只依赖 domain。
type SearchTuning struct {
	BM25Top       int     // 每路召回条数
	KNNTops       int
	NumCandidates int     // kNN 的候选池，越大越准越慢
	RankConstant  int     // RRF 的 k，论文默认 60
	DenseWeight   float64 // 默认向量路权重
}

// WithDefaults 补齐零值，避免配置漏填导致召回为 0。
func (t SearchTuning) WithDefaults() SearchTuning {
	out := t
	if out.BM25Top <= 0 {
		out.BM25Top = 100
	}
	if out.KNNTops <= 0 {
		out.KNNTops = 100
	}
	if out.NumCandidates <= 0 {
		out.NumCandidates = out.KNNTops * 5
	}
	if out.RankConstant <= 0 {
		out.RankConstant = 60
	}
	if out.DenseWeight <= 0 {
		out.DenseWeight = 0.5
	}
	return out
}
