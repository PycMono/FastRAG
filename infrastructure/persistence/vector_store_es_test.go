package persistence

import (
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/domain/repository"
)

// 检索请求的构造是纯函数，不需要真连 ES——但它错起来是**静默**的：
// 少一条子句只是召回变少，filter 挂错位置只是越权，两者都不会报错。
// 这里把这两条约定钉住。
func TestKNNClauses(t *testing.T) {
	store := &ESVectorStore{index: "t"}

	req := repository.VectorSearchReq{
		Account:       "demo",
		KBIDs:         []uint64{1, 2},
		QueryVec:      []float32{0.1, 0.2},
		KNNTops:       50,
		NumCandidates: 200,
	}

	t.Run("content 模式发标题+正文两条子句", func(t *testing.T) {
		clauses := store.knnClauses(req)
		if len(clauses) != 2 {
			t.Fatalf("期望 2 条 kNN 子句，得到 %d 条", len(clauses))
		}

		// 顺序是 title_vec 在前、content_vec 在后。
		// 顺序本身不影响 ES（分数相加，可交换），钉住它是为了 diff 时好读
		want := []string{constants.FieldTitleVec, constants.FieldContentVec}
		for i, c := range clauses {
			got := c.(map[string]any)["field"]
			if got != want[i] {
				t.Errorf("第 %d 条子句的 field = %v，期望 %v", i, got, want[i])
			}
		}
	})

	t.Run("title 模式只发标题一条", func(t *testing.T) {
		titleOnly := req
		titleOnly.TitleOnly = true

		clauses := store.knnClauses(titleOnly)
		if len(clauses) != 1 {
			t.Fatalf("期望 1 条 kNN 子句，得到 %d 条", len(clauses))
		}
		if got := clauses[0].(map[string]any)["field"]; got != constants.FieldTitleVec {
			t.Errorf("field = %v，期望 %v", got, constants.FieldTitleVec)
		}
	})

	t.Run("num_candidates 不小于 k", func(t *testing.T) {
		// num_candidates < k 时 ES 直接 400，所以必须被抬到 k
		small := req
		small.KNNTops = 50
		small.NumCandidates = 10

		for i, c := range store.knnClauses(small) {
			m := c.(map[string]any)
			if m["num_candidates"].(int) < m["k"].(int) {
				t.Errorf("第 %d 条子句 num_candidates=%v < k=%v",
					i, m["num_candidates"], m["k"])
			}
		}
	})

	t.Run("每条子句都带租户过滤", func(t *testing.T) {
		// filter 只挂请求外层是不够的：每条 kNN 子句各自独立召回，
		// 漏挂一条就是那条子句跨租户（D2）
		for i, c := range store.knnClauses(req) {
			filters, ok := c.(map[string]any)["filter"].([]any)
			if !ok || len(filters) == 0 {
				t.Fatalf("第 %d 条子句没带 filter", i)
			}

			first, _ := filters[0].(map[string]any)
			term, _ := first["term"].(map[string]any)
			if term[constants.FieldAccount] != "demo" {
				t.Errorf("第 %d 条子句的 account 过滤 = %v，期望 term/demo",
					i, filters[0])
			}
		}
	})
}

// 日志摘要必须认得 knn 的两种形态。认不出数组形态的后果是把 1024 个 float32
// 原样打进日志——而那正是排查「走没走向量路」时唯一能看的东西。
func TestSummarizeQueryVec(t *testing.T) {
	vec := make([]float32, 1024)
	vec[0], vec[1023] = 0.5, -0.25

	clause := func(field string) map[string]any {
		return map[string]any{
			"field":        field,
			"query_vector": vec,
			"k":            50,
		}
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"数组形态（content 模式）", map[string]any{
			"knn": []any{clause(constants.FieldTitleVec), clause(constants.FieldContentVec)},
		}},
		{"单对象形态（title 模式）", map[string]any{
			"knn": clause(constants.FieldTitleVec),
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := summarizeQueryVec(c.body)

			// ① 原 body 必须原封不动：它随后还要发给 ES，
			//    就地改成字符串的话，发出去的请求体就是坏的
			for _, cl := range clausesOf(c.body["knn"]) {
				if _, ok := cl["query_vector"].([]float32); !ok {
					t.Fatal("原 body 的 query_vector 被就地改掉了")
				}
			}

			// ② 摘要里每一路都得是字符串，且带维度
			summarized := clausesOf(out["knn"])
			if len(summarized) == 0 {
				t.Fatal("摘要里一条子句都没有")
			}
			for _, cl := range summarized {
				s, ok := cl["query_vector"].(string)
				if !ok {
					t.Fatalf("query_vector 没被换成字符串，而是 %T", cl["query_vector"])
				}
				if !strings.Contains(s, "1024 维") {
					t.Errorf("摘要里没带维度: %q", s)
				}
			}
		})
	}
}

// IndexName 是配置留空时的兜底索引名。它必须与
// scripts/create-es-index.sh 里的 INDEX 默认值一致 —— 两边不一致的话，
// 服务会去读写一个脚本从没建过的索引。
func TestIndexName_SingleIndex(t *testing.T) {
	if constants.IndexName != "fastrag" {
		t.Errorf("IndexName = %q, want fastrag", constants.IndexName)
	}
}

// smokeCheckMapping 是「写入/检索前的最后一道闸」。它只在索引明显不是我们的
// 时候报警（ES 的 auto_create_index 会静默建出动态 mapping 的索引）。
// 这里钉住它不误报、也不漏报。
func TestSmokeCheckMapping(t *testing.T) {
	good := map[string]esField{
		constants.FieldAccount:    {Type: "keyword"},
		constants.FieldContentVec: {Type: "dense_vector", Dims: 1024},
	}

	t.Run("正常索引不报", func(t *testing.T) {
		if got := smokeCheckMapping(good); got != "" {
			t.Errorf("不该报错，得到 %q", got)
		}
	})

	t.Run("动态 mapping 建出来的索引要报", func(t *testing.T) {
		// ES 把 account 动态映射成 text（带 keyword 子字段），content_vec 根本不存在
		bad := map[string]esField{constants.FieldAccount: {Type: "text"}}
		got := smokeCheckMapping(bad)
		if got == "" {
			t.Fatal("必须报错，否则跨租户越权会静默发生")
		}
		if !strings.Contains(got, "keyword") {
			t.Errorf("错误信息要点出 account 应为 keyword，得到 %q", got)
		}
	})

	t.Run("向量字段被建成别的类型要报", func(t *testing.T) {
		bad := map[string]esField{
			constants.FieldAccount:    {Type: "keyword"},
			constants.FieldContentVec: {Type: "float"},
		}
		got := smokeCheckMapping(bad)
		if !strings.Contains(got, "dense_vector") {
			t.Errorf("错误信息要点出 content_vec 应为 dense_vector，得到 %q", got)
		}
	})
}

// clausesOf 把摘要结果里的 knn 统一成切片，两种形态都吃。
func clausesOf(knn any) []map[string]any {
	switch v := knn.(type) {
	case map[string]any:
		return []map[string]any{v}
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, c := range v {
			if m, ok := c.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
