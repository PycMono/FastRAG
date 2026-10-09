package serviceimpl

import (
	"encoding/json"
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
