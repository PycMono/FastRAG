package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
)

// ChunkID 由 (docID, order, content) 派生。
//
// 纯函数、可复现，内容一变 ID 就变。因此重导入时旧切片不会被覆盖，
// 必须由 §5.2 第 ② 步按 _id 差集显式清掉；内容没变的那部分 ID 不变，
// 原地覆盖、也不会被删——差集清理对它们是零成本的。
func ChunkID(docID uint64, order int, content string) string {
	h := sha256.New()
	h.Write([]byte(strconv.FormatUint(docID, 10)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(order)))
	h.Write([]byte{0})
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// BuildVectorDocs 把领域切片 + 已算好的向量，组装成存储中立的 VectorDoc。
//
// 向量由调用方（应用层）通过 IEmbedding 算好再传进来，
// 工厂只做组装——它不碰网络，所以可测。
//
// 返回的 ChunkID 列表就是 §5.2 第 ② 步「保留集合」的来源，
// 调用方不用自己再算一遍（算两遍就多一处可能算岔的地方）。
func BuildVectorDocs(
	kb *entity.KnowledgeBase,
	docID uint64,
	bizTag string,
	chunks entity.Chunks,
	titleVecs, contentVecs [][]float32,
	now time.Time,
) []repository.VectorDoc {
	if len(titleVecs) != len(chunks) || len(contentVecs) != len(chunks) {
		// 向量条数与切片数必须一一对应；对不上属于调用方 bug，
		// 直接 panic 比写出一半脏数据好
		panic("factory: 向量条数与切片数不一致")
	}

	ts := now.UnixMilli()
	docs := make([]repository.VectorDoc, len(chunks))
	for i, c := range chunks {
		docs[i] = repository.VectorDoc{
			ChunkID:     ChunkID(docID, c.Order, c.Content),
			Account:     kb.Account,
			KBID:        kb.ID,
			DocID:       docID,
			Order:       c.Order,
			BizTag:      bizTag,
			Title:       c.Title,
			Content:     c.Content,
			HeadingPath: c.HeadingPath,
			TitleVec:    titleVecs[i],
			ContentVec:  contentVecs[i],
			CreateTs:    ts,
		}
	}
	return docs
}

// ChunkIDs 取出切片 ID 列表，供 §5.2 第 ② 步的差集清理使用。
func ChunkIDs(docs []repository.VectorDoc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ChunkID
	}
	return out
}

// EmbeddingTexts 返回喂给 embedding 的文本。
//
// 有标题链时用「标题链 + 正文」，让上下文信息自然地进 embedding——
// 这就是第 §8.1 步那个廉价版 Contextual Retrieval 的落点。
func EmbeddingTexts(chunks entity.Chunks) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Content // 结构感知切片已把标题链写进 Content 开头
	}
	return out
}
