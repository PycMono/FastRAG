package entity

// Chunk 切片。
//
// 它不是聚合根——切片没有独立生命周期，完全从属于文档（D3）。
type Chunk struct {
	Order       int    // 文档内序号，从 0 起
	Title       string // 切片标题（通常是最近一级标题）
	HeadingPath string // 标题链，"产品介绍，概述，配置方法"
	Content     string // 正文（已含标题链 prefix）
}

// Chunks 切片集合。
type Chunks []*Chunk

// Titles 取出所有切片标题，供 embedding 批量调用使用。
func (cs Chunks) Titles() []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Title
	}
	return out
}

// Contents 取出所有切片正文。
func (cs Chunks) Contents() []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Content
	}
	return out
}

// TotalChars 总字符数，用于日志与容量预估。
func (cs Chunks) TotalChars() int {
	n := 0
	for _, c := range cs {
		n += len([]rune(c.Content))
	}
	return n
}
