package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// newRerankTestServer 创建一个 mock /rerank 服务。
// handler 接收请求并返回自定义响应；返回 server 和实际调用次数（原子计数）。
func newRerankTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *int32) {
	var calls int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		handler(w, r)
	})), &calls
}

// newTestRerank 用 mock server 构造 Rerank。
//
// url 存的是完整端点，所以要自己带上 /rerank 后缀——
// 这正是新配置形状的要求（不再由实现拼后缀）。
func newTestRerank(server *httptest.Server, batchSize, concurrency int) *Rerank {
	return &Rerank{
		client:      http.DefaultClient,
		url:         server.URL + "/rerank",
		model:       "test-model",
		batchSize:   batchSize,
		concurrency: concurrency,
		defaultTopN: 0,
	}
}

// callCount 读取原子计数器的当前值。
func callCount(p *int32) int {
	return int(atomic.LoadInt32(p))
}

func TestRerank_Rerank_Basic(t *testing.T) {
	server, calls := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("期望路径 /rerank，得到 %s", r.URL.Path)
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode 请求失败: %v", err)
		}
		docs := req["documents"].([]any)

		results := make([]map[string]any, len(docs))
		for i := range docs {
			// index 0 最高分，越往后越低
			results[i] = map[string]any{
				"index":           i,
				"relevance_score": float64(len(docs)-i) / float64(len(docs)),
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{
		{ChunkID: "c1", Content: "aaa"},
		{ChunkID: "c2", Content: "bbb"},
		{ChunkID: "c3", Content: "ccc"},
	}

	order, err := r.Rerank(context.Background(), "query", cands, 2)
	if err != nil {
		t.Fatalf("Rerank 不应失败: %v", err)
	}
	if callCount(calls) != 1 {
		t.Errorf("期望调用 1 次，调用 %d 次", callCount(calls))
	}
	want := []int{0, 1}
	if len(order) != len(want) {
		t.Fatalf("期望返回 %d 条，得到 %d 条", len(want), len(order))
	}
	for i, idx := range order {
		if idx.Index != want[i] {
			t.Errorf("order[%d].Index = %d，期望 %d", i, idx.Index, want[i])
		}
	}
	// 上游分要原样带出来。stub 对 3 条候选给 index0=1.0、index1=2/3，
	// 取前 2 条就该是这两个分——调用方靠它替换融合分。
	if order[0].Score != 1.0 {
		t.Errorf("order[0].Score 应为上游分 1.0，得到 %v", order[0].Score)
	}
	if want := 2.0 / 3.0; order[1].Score != want {
		t.Errorf("order[1].Score 应为上游分 %v，得到 %v", want, order[1].Score)
	}
}

func TestRerank_Rerank_EmptyCandidates(t *testing.T) {
	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应调用服务端")
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	order, err := r.Rerank(context.Background(), "query", nil, 10)
	if err != nil {
		t.Fatalf("空候选不应失败: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("空候选应返回空，得到 %v", order)
	}
}

func TestRerank_Rerank_EmptyContent_Sinks(t *testing.T) {
	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		docs := req["documents"].([]any)

		results := make([]map[string]any, len(docs))
		for i := range docs {
			results[i] = map[string]any{
				"index":           i,
				"relevance_score": 0.8,
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{
		{ChunkID: "c1", Content: "valid content"},
		{ChunkID: "c2", Content: ""}, // 空内容
	}

	order, err := r.Rerank(context.Background(), "query", cands, 2)
	if err != nil {
		t.Fatalf("Rerank 不应失败: %v", err)
	}
	// 空内容应沉底
	if order[0].Index != 0 || order[1].Index != 1 {
		t.Errorf("空内容应沉底，得到 order=%v", order)
	}
	// 有效的那条带上游分；空内容那条没有上游分，给哨兵——
	// 调用方据此保留原融合分，而不是把 0 分写进展示字段。
	if order[0].Score != 0.8 {
		t.Errorf("有效候选应带上游分 0.8，得到 %v", order[0].Score)
	}
	if order[1].Score != interfaces.NoRerankScore {
		t.Errorf("空内容候选应为哨兵分 %d，得到 %v", interfaces.NoRerankScore, order[1].Score)
	}
}

func TestRerank_Rerank_Batching(t *testing.T) {
	server, calls := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		docs := req["documents"].([]any)

		// 每批内按 index 降序给分；跨批时同分会按加入顺序稳定排序，
		// 所以 batch0 的 index 0 会排在 batch1 的 index 0 前面。
		results := make([]map[string]any, len(docs))
		for i := range docs {
			results[i] = map[string]any{
				"index":           i,
				"relevance_score": float64(len(docs) - i),
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	defer server.Close()

	batchSize := 10
	total := 25 // 3 批：10 + 10 + 5
	r := newTestRerank(server, batchSize, 4)
	cands := make([]interfaces.RerankCandidate, total)
	for i := range cands {
		cands[i] = interfaces.RerankCandidate{ChunkID: fmt.Sprintf("c%d", i), Content: fmt.Sprintf("doc%d", i)}
	}

	order, err := r.Rerank(context.Background(), "query", cands, total)
	if err != nil {
		t.Fatalf("Rerank 不应失败: %v", err)
	}
	if callCount(calls) != 3 {
		t.Errorf("期望调用 3 次，调用 %d 次", callCount(calls))
	}
	if len(order) != total {
		t.Fatalf("期望返回 %d 条，得到 %d 条", total, len(order))
	}
	// 稳定排序后，同分批次内保持原始加入顺序：
	// batch0 的 0,1,2... 与 batch1 的 10,11,12... 交替出现。
	wantFirst := []int{0, 10, 1, 11, 2, 12}
	for i, want := range wantFirst {
		if order[i].Index != want {
			t.Errorf("order[%d].Index = %d，期望 %d", i, order[i].Index, want)
		}
	}
}

func TestRerank_Rerank_OutOfRangeIndex_Ignored(t *testing.T) {
	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.9},
				{"index": 99, "relevance_score": 0.8}, // 越界
				{"index": 1, "relevance_score": 0.7},
			},
		})
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{
		{ChunkID: "c1", Content: "a"},
		{ChunkID: "c2", Content: "b"},
	}

	order, err := r.Rerank(context.Background(), "query", cands, 2)
	if err != nil {
		t.Fatalf("Rerank 不应失败: %v", err)
	}
	// index 0 最高，index 1 次之，越界 index 被忽略
	want := []int{0, 1}
	for i, idx := range order {
		if idx.Index != want[i] {
			t.Errorf("order[%d].Index = %d，期望 %d", i, idx.Index, want[i])
		}
	}
}

func TestRerank_Rerank_ServerError(t *testing.T) {
	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "boom"},
		})
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{{ChunkID: "c1", Content: "a"}}

	_, err := r.Rerank(context.Background(), "query", cands, 1)
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if err.Error() != "rerank 服务返回错误: boom" && err.Error() != "rerank 服务 HTTP 500: {\"error\":{\"message\":\"boom\"}}" {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestRerank_Rerank_RetryOnce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "temporary"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.9},
			},
		})
	}))
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{{ChunkID: "c1", Content: "a"}}

	order, err := r.Rerank(context.Background(), "query", cands, 1)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if calls != 2 {
		t.Errorf("期望调用 2 次（初次失败 + 1 次重试），实际 %d 次", calls)
	}
	if len(order) != 1 || order[0].Index != 0 {
		t.Errorf("重排结果错误: %v", order)
	}
}

func TestRerank_Rerank_TopN(t *testing.T) {
	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		docs := req["documents"].([]any)

		results := make([]map[string]any, len(docs))
		for i := range docs {
			results[i] = map[string]any{
				"index":           i,
				"relevance_score": float64(len(docs) - i),
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	defer server.Close()

	r := newTestRerank(server, 32, 4)
	cands := []interfaces.RerankCandidate{
		{ChunkID: "c1", Content: "a"},
		{ChunkID: "c2", Content: "b"},
		{ChunkID: "c3", Content: "c"},
	}

	order, err := r.Rerank(context.Background(), "query", cands, 2)
	if err != nil {
		t.Fatalf("Rerank 不应失败: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("期望返回 2 条，得到 %d 条", len(order))
	}
	if order[0].Index != 0 || order[1].Index != 1 {
		t.Errorf("topN 截取错误: %v", order)
	}
}

func TestRerank_CandidateText(t *testing.T) {
	r := &Rerank{}
	cases := []struct {
		name string
		cand interfaces.RerankCandidate
		want string
	}{
		{
			name: "全有",
			cand: interfaces.RerankCandidate{Title: "标题", HeadingPath: "A > B", Content: "正文"},
			want: "标题\nA > B\n正文",
		},
		{
			name: "无标题",
			cand: interfaces.RerankCandidate{HeadingPath: "A > B", Content: "正文"},
			want: "A > B\n正文",
		},
		{
			name: "只有正文",
			cand: interfaces.RerankCandidate{Content: "正文"},
			want: "正文",
		},
		{
			name: "空",
			cand: interfaces.RerankCandidate{},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.candidateText(tc.cand)
			if got != tc.want {
				t.Errorf("candidateText() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// ─── dashscope 协议 ─────────────────────────────────────────────────────────

func TestRerank_BuildBody_OpenAI(t *testing.T) {
	body, err := json.Marshal((&Rerank{model: "test-model"}).buildBody("q", []string{"d1", "d2"}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["query"] != "q" {
		t.Errorf("query 应原样带上，得到 %v", got["query"])
	}
	docs, ok := got["documents"].([]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("openai 的 documents 应是裸数组，得到 %#v", got["documents"])
	}
	if _, has := got["parameters"]; has {
		t.Error("openai 这一路不发 parameters，也就不该有 top_n")
	}
}

func TestRerank_BuildBody_DashScope(t *testing.T) {
	r := &Rerank{model: "test-model"}
	r.protocol = config.ProtocolDashScope

	body, err := json.Marshal(r.buildBody("q", []string{"d1", "d2", "d3"}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 的 input 应是 {query, documents}，得到 %#v", got["input"])
	}
	if input["query"] != "q" {
		t.Errorf("input.query 应原样带上，得到 %v", input["query"])
	}
	if docs, ok := input["documents"].([]any); !ok || len(docs) != 3 {
		t.Errorf("input.documents 应是 3 条，得到 %#v", input["documents"])
	}

	params, ok := got["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 这一路必须带 parameters，得到 %#v", got["parameters"])
	}
	// top_n 必须是本批长度，不是业务上的 topN
	if params["top_n"] != float64(3) {
		t.Errorf("top_n 应等于本批文档数 3，得到 %v", params["top_n"])
	}
}

// 实测响应：{"output":{"results":[{"index":..,"relevance_score":..}]},...}
func TestRerank_ParseScores_DashScope(t *testing.T) {
	var parsed rerankResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"results":[{"index":1,"relevance_score":0.1},`+
			`{"index":0,"relevance_score":0.9}]},"usage":{},"request_id":"x"}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	r := &Rerank{model: "test-model"}
	r.protocol = config.ProtocolDashScope
	got := r.parseScores(&parsed, 2)

	if len(got) != 2 || got[0] != 0.9 || got[1] != 0.1 {
		t.Errorf("应按 index 回填 output.results，得到 %v", got)
	}
}

// §4.3 那条坑：dashscope 的 parameters.top_n 是"只返回前 N 条"，
// 写小了这一批剩下的候选就没分，批与批之间的分数不再可比，合并后顺序是乱的——
// 而且不报错。所以分批时它必须恒等于本批长度。
func TestRerank_DashScope_TopNEqualsBatchSize(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []int
	)

	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Parameters struct {
				TopN int `json:"top_n"`
			} `json:"parameters"`
			Input struct {
				Documents []string `json:"documents"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		seen = append(seen, req.Parameters.TopN)
		if req.Parameters.TopN != len(req.Input.Documents) {
			t.Errorf("top_n=%d 与本批 documents=%d 不等", req.Parameters.TopN, len(req.Input.Documents))
		}
		mu.Unlock()

		results := make([]map[string]any, len(req.Input.Documents))
		for i := range req.Input.Documents {
			results[i] = map[string]any{"index": i, "relevance_score": 1.0 - float64(i)*0.1}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{"results": results},
		})
	})
	defer server.Close()

	r := newTestRerank(server, 2, 1)
	r.protocol = config.ProtocolDashScope
	r.url = server.URL // dashscope 用完整端点，不拼后缀

	cands := make([]interfaces.RerankCandidate, 5)
	for i := range cands {
		cands[i] = interfaces.RerankCandidate{ChunkID: fmt.Sprintf("c%d", i), Content: "内容"}
	}

	order, err := r.Rerank(context.Background(), "q", cands, 0)
	if err != nil {
		t.Fatalf("Rerank 失败: %v", err)
	}
	if len(order) != 5 {
		t.Fatalf("应返回 5 个下标，得到 %d", len(order))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("5 条候选按 batchSize=2 应发 3 批，实际 %d 批", len(seen))
	}
	for _, n := range seen {
		if n != 2 && n != 1 { // 最后一批只剩 1 条
			t.Errorf("top_n 只能是本批长度（2 或 1），得到 %d", n)
		}
	}
}
