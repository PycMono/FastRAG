package service

// ChunkInput 调用方直接给出的裸切片（format = chunks）。
//
// 不用 common/dto 的 ChunkDTO：domain 不该认识传输层结构，
// 由 controller → application 时做一次转换。
type ChunkInput struct {
	Title   string
	Content string
}
