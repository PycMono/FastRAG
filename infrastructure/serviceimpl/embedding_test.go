package serviceimpl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/config"
)

// newTestEmbedding 造一个只用于拼请求体和解析响应的 embedding，不发任何请求。
func newTestEmbedding(protocol string) *Embedding {
	return &Embedding{protocol: protocol, model: "test-model"}
}

func TestEmbedding_BuildBody_OpenAI(t *testing.T) {
	body, err := json.Marshal(newTestEmbedding("").buildBody([]string{"a", "b"}, textTypeDocument))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["model"] != "test-model" {
		t.Errorf("model 应原样带上，得到 %v", got["model"])
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("openai 的 input 应是裸数组，得到 %#v", got["input"])
	}
	if _, has := got["parameters"]; has {
		t.Error("openai 这一路不该发 parameters")
	}
}

func TestEmbedding_BuildBody_DashScope(t *testing.T) {
	body, err := json.Marshal(newTestEmbedding(config.ProtocolDashScope).
		buildBody([]string{"a"}, textTypeQuery))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 的 input 应是 {texts:[...]}，得到 %#v", got["input"])
	}
	if texts, ok := input["texts"].([]any); !ok || len(texts) != 1 {
		t.Errorf("input.texts 应是一条，得到 %#v", input["texts"])
	}

	params, ok := got["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 这一路必须带 parameters，得到 %#v", got["parameters"])
	}
	if params["text_type"] != textTypeQuery {
		t.Errorf("text_type 应是 %q，得到 %v", textTypeQuery, params["text_type"])
	}
}

// 实测响应：{"data":[{"index":..,"embedding":[..]}],...}
func TestEmbedding_ParseVectors_OpenAI(t *testing.T) {
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"data":[{"index":1,"embedding":[0.1,0.2]},{"index":0,"embedding":[0.3,0.4]}],"usage":{}}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	got := newTestEmbedding("").parseVectors(&parsed, 2)
	if len(got) != 2 {
		t.Fatalf("应回填 2 条，得到 %d", len(got))
	}
	// 按 index 回填，不信返回数组的顺序
	if got[0][0] != 0.3 || got[1][0] != 0.1 {
		t.Errorf("应按 index 回填，得到 %v", got)
	}
}

// 实测响应：{"output":{"embeddings":[{"text_index":..,"embedding":[..]}]},...}
func TestEmbedding_ParseVectors_DashScope(t *testing.T) {
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"embeddings":[{"text_index":1,"embedding":[0.1]},{"text_index":0,"embedding":[0.2]}]},`+
			`"usage":{},"request_id":"x"}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	got := newTestEmbedding(config.ProtocolDashScope).parseVectors(&parsed, 2)
	if len(got) != 2 {
		t.Fatalf("应回填 2 条，得到 %d", len(got))
	}
	if got[0][0] != 0.2 || got[1][0] != 0.1 {
		t.Errorf("应按 text_index 回填，得到 %v", got)
	}
}

// 两个协议的下标字段名不同：openai 是 index，dashscope 是 text_index——
// 但它们**都是 int**，而 int 字段缺失时 JSON 解码器给的是 0，0 又是一个合法下标。
//
// 于是共用一个解析器不会报错、也不会丢向量，而是把所有向量都堆到第 0 条上
// （§4.1 说的"抄错一个就是把所有向量挂到第 0 条"）。这个测试盯住的就是这条：
// 每一边只认自己的字段名，另一边的下标一律不生效。
//
// 断言落在"除第 0 条外仍为空"上：第 0 条两种情况都会有值，是区分不出来的那一格。
func TestEmbedding_ParseVectors_IndexFieldsNotInterchangeable(t *testing.T) {
	// dashscope 这一路只认 text_index。只给 index 时，第 1 条应仍为空。
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"embeddings":[{"index":0,"embedding":[0.9]},{"index":1,"embedding":[0.8]}]}}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}
	got := newTestEmbedding(config.ProtocolDashScope).parseVectors(&parsed, 2)
	if len(got[1]) != 0 {
		t.Errorf("dashscope 这一路不该认 index 字段：第 1 条应仍为空，得到 %v", got[1])
	}

	// 反过来：openai 这一路只认 index。只给 text_index 时，第 1 条应仍为空。
	parsed = embedResponse{}
	if err := json.Unmarshal([]byte(
		`{"data":[{"text_index":0,"embedding":[0.9]},{"text_index":1,"embedding":[0.8]}]}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}
	got = newTestEmbedding("").parseVectors(&parsed, 2)
	if len(got[1]) != 0 {
		t.Errorf("openai 这一路不该认 text_index 字段：第 1 条应仍为空，得到 %v", got[1])
	}
}

// ─── 空文本不得发往上游 ───────────────────────────────────────────────────────

// newEmbedTestServer 起一个假的上游，把每次请求收到的 texts 记下来。
func newEmbedTestServer(t *testing.T) (*httptest.Server, *[][]string) {
	var got [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode 请求失败: %v", err)
			return
		}
		got = append(got, req.Input)

		out := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			out[i] = map[string]any{"index": i, "embedding": []float32{1, 2, 3}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(server.Close)
	return server, &got
}

// 空文本（含纯空白）一个都不能发出去，但返回条数必须与入参一一对应。
//
// 为什么：DashScope 收到 texts 里任何一个空串，**整批**都会改用一个别的模型、
// 返回 2560 维（实测 2026-10-09，只有 "" 触发，" " 和 "\n" 不触发）。而
// format=text 的切片标题恒为空，也就等于纯文本导入必然踩中——报出来的是
// "维度为 2560"，指不到真正的原因。
func TestEmbedding_EmbedDocs_EmptyTextsNeverReachUpstream(t *testing.T) {
	server, got := newEmbedTestServer(t)
	e := &Embedding{
		client:    http.DefaultClient,
		url:       server.URL,
		model:     "test-model",
		dim:       3,
		batchSize: 2, // 故意设小，逼出跨批次的回填
	}

	texts := []string{"标题A", "", "标题B", "   ", "标题C"}
	vecs, err := e.EmbedDocs(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedDocs 不应失败: %v", err)
	}

	if len(vecs) != len(texts) {
		t.Fatalf("返回条数必须与入参一一对应：期望 %d，得到 %d", len(texts), len(vecs))
	}

	for _, batch := range *got {
		for _, s := range batch {
			if strings.TrimSpace(s) == "" {
				t.Errorf("空文本不该发往上游，却发出去了：%q（本批 %q）", s, batch)
			}
		}
	}

	// 非空的位置拿到的是上游的真实向量，且顺序没被批次的切分打乱。
	for _, i := range []int{0, 2, 4} {
		if len(vecs[i]) != 3 {
			t.Errorf("第 %d 条应拿到上游向量（3 维），得到 %v", i, vecs[i])
		}
	}
	// 空的位置留 nil。
	//
	// 不能补零向量：ES 的 cosine 明确拒绝零模长向量，会让整批 bulk 400
	// （比原来的问题还难查）。nil 序列化成 null，ES 当作字段不存在。
	for _, i := range []int{1, 3} {
		if vecs[i] != nil {
			t.Errorf("第 %d 条应留 nil（不写 title_vec），得到 %#v", i, vecs[i])
		}
	}
}

// 整批都是空文本时也得返回等长的 nil 切片——
// format=text 的标题那一路就是这种情况：全空，且一条都不该发出去。
func TestEmbedding_EmbedDocs_AllEmpty(t *testing.T) {
	server, got := newEmbedTestServer(t)
	e := &Embedding{client: http.DefaultClient, url: server.URL, model: "m", dim: 3, batchSize: 10}

	vecs, err := e.EmbedDocs(context.Background(), []string{"", "  "})
	if err != nil {
		t.Fatalf("EmbedDocs 不应失败: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(vecs))
	}
	if vecs[0] != nil || vecs[1] != nil {
		t.Errorf("两条都该是 nil，得到 %#v", vecs)
	}
	if len(*got) != 0 {
		t.Errorf("全是空文本时一个请求都不该发，却发了 %d 次：%q", len(*got), *got)
	}
}

// ─── 重试 ─────────────────────────────────────────────────────────────────────

// newFlakyEmbedServer 起一个会抖的上游：前 failTimes 次请求返回 500，之后正常。
// 返回 server 与调用计数器。
func newFlakyEmbedServer(t *testing.T, failTimes int32) (*httptest.Server, *int32) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= failTimes {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "temporary"},
			})
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			out[i] = map[string]any{"index": i, "embedding": []float32{1, 2, 3}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// 上游第一次 500、第二次成功：应重试一次后成功，而不是把整次导入带崩。
//
// 这就是这次改动的全部目的——EmbedDocs 逐批发送，任何一批抖一下，在加重试前
// 整次导入都会失败，且前面已算好的批次全部作废，调用方只能从头再导。
func TestEmbedding_EmbedDocs_RetryOnce(t *testing.T) {
	server, calls := newFlakyEmbedServer(t, 1)
	e := &Embedding{client: http.DefaultClient, url: server.URL, model: "m", dim: 3, batchSize: 10}

	vecs, err := e.EmbedDocs(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if got := int(atomic.LoadInt32(calls)); got != 2 {
		t.Errorf("期望调用 2 次（初次失败 + 1 次重试），实际 %d 次", got)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Errorf("应拿到上游向量，得到 %#v", vecs)
	}
}

// 重试有上限：两次都失败就报错，绝不无限重试。
func TestEmbedding_EmbedDocs_RetryExhausted(t *testing.T) {
	server, calls := newFlakyEmbedServer(t, 100) // 永远失败
	e := &Embedding{client: http.DefaultClient, url: server.URL, model: "m", dim: 3, batchSize: 10}

	_, err := e.EmbedDocs(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("两次都失败时应报错")
	}
	if got := int(atomic.LoadInt32(calls)); got != 2 {
		t.Errorf("重试次数必须是 2（初次 + 1），实际 %d", got)
	}
}
