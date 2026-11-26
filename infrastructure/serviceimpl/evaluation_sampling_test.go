package serviceimpl

import (
	"fmt"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestSamplingBalancesDocumentsAndSpreadsChapters(t *testing.T) {
	groups := make([][]entity.EvalSourceChunk, 2)
	for document := range groups {
		for position := 0; position < 1000; position++ {
			groups[document] = append(groups[document], entity.EvalSourceChunk{
				DocID:       fmt.Sprint(document),
				ChunkID:     fmt.Sprintf("%d-%d", document, position),
				HeadingPath: fmt.Sprintf("chapter-%d", position/10),
				Order:       position,
			})
		}
	}

	selected := sampleEvaluationSources(groups, entity.EvalSamplePolicy{Total: 150})
	if len(selected) != 150 {
		t.Fatalf("got %d", len(selected))
	}
	counts := map[string]int{}
	chapters := map[string]bool{}
	for _, source := range selected {
		counts[source.DocID]++
		chapters[source.DocID+source.HeadingPath] = true
	}
	if counts["0"] != 75 || counts["1"] != 75 || len(chapters) != 150 {
		t.Fatalf("分层不均衡: %+v chapters=%d", counts, len(chapters))
	}
	if selected[0].Order > 20 || selected[74].Order < 970 {
		t.Fatal("样本没有覆盖文档前后位置")
	}
}

func TestSamplingBalancesPerDocCapAndTotalCap(t *testing.T) {
	groups := make([][]entity.EvalSourceChunk, 3)
	for document := range groups {
		for position := 0; position < 1000; position++ {
			groups[document] = append(groups[document], entity.EvalSourceChunk{
				DocID:       fmt.Sprint(document),
				ChunkID:     fmt.Sprintf("%d-%d", document, position),
				HeadingPath: fmt.Sprintf("chapter-%d", position/10),
				Order:       position,
			})
		}
	}

	selected := sampleEvaluationSources(groups, entity.EvalSamplePolicy{PerDoc: 50, Total: 100})
	if len(selected) != 100 {
		t.Fatalf("got %d", len(selected))
	}
	counts := map[string]int{}
	for _, source := range selected {
		counts[source.DocID]++
	}
	if counts["0"] != 34 || counts["1"] != 33 || counts["2"] != 33 {
		t.Fatalf("按篇均衡失败: %+v", counts)
	}
	for docID, count := range counts {
		if count > 50 {
			t.Fatalf("doc %s 超过单篇上限: %d", docID, count)
		}
	}

	one := sampleEvaluationSources(groups[:1], entity.EvalSamplePolicy{PerDoc: 50, Total: 100})
	if len(one) != 50 {
		t.Fatalf("单篇上限失败: %d", len(one))
	}
	two := sampleEvaluationSources(groups[:2], entity.EvalSamplePolicy{PerDoc: 50, Total: 100})
	if len(two) != 100 {
		t.Fatalf("两篇总数失败: %d", len(two))
	}
}

func TestSamplingEmptyAndOverRequested(t *testing.T) {
	if len(sampleEvaluationSources(nil, entity.EvalSamplePolicy{Total: 150})) != 0 {
		t.Fatal("空语料产生样本")
	}
	groups := [][]entity.EvalSourceChunk{{{ChunkID: "1"}, {ChunkID: "2"}}}
	if len(sampleEvaluationSources(groups, entity.EvalSamplePolicy{Total: 150})) != 2 {
		t.Fatal("超出数量时重复取样")
	}
}
