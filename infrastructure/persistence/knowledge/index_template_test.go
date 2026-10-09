package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
)

// propsOf 把 body 里的 mappings.properties 取出来。
// 取不到就 Fatal —— body 结构错了，后面所有断言都没有意义。
func propsOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	mappings, ok := body["mappings"].(map[string]any)
	if !ok {
		t.Fatalf("body[mappings] 类型为 %T, 期望 map[string]any", body["mappings"])
	}
	props, ok := mappings["properties"].(map[string]any)
	if !ok {
		t.Fatalf("mappings[properties] 类型为 %T, 期望 map[string]any", mappings["properties"])
	}
	return props
}

func settingsOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	settings, ok := body["settings"].(map[string]any)
	if !ok {
		t.Fatalf("body[settings] 类型为 %T, 期望 map[string]any", body["settings"])
	}
	return settings
}

func objectField(t *testing.T, props map[string]any, field string) map[string]any {
	t.Helper()

	v, ok := props[field].(map[string]any)
	if !ok {
		t.Fatalf("字段 %s 类型为 %T, 期望 object", field, props[field])
	}
	return v
}

func TestBuildCreateIndexBody_Defaults(t *testing.T) {
	body, err := BuildCreateIndexBody(DefaultIndexSpec())
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}

	settings := settingsOf(t, body)
	if got := settings["number_of_shards"]; got != 3 {
		t.Errorf("number_of_shards = %v, want 3", got)
	}
	if got := settings["number_of_replicas"]; got != 1 {
		t.Errorf("number_of_replicas = %v, want 1", got)
	}
	if got := settings["refresh_interval"]; got != "30s" {
		t.Errorf("refresh_interval = %v, want 30s", got)
	}

	props := propsOf(t, body)

	// 两个向量字段必须都是 dense_vector 且维度与 spec 一致。
	// 这是整张表最容易写错、又最晚才暴露的地方——向量都灌进去了才发现维度对不上。
	for _, f := range []string{FieldTitleVec, FieldContentVec} {
		v := objectField(t, props, f)
		if v["type"] != "dense_vector" {
			t.Errorf("%s.type = %v, want dense_vector", f, v["type"])
		}
		if v["dims"] != 1024 {
			t.Errorf("%s.dims = %v, want 1024", f, v["dims"])
		}
		if v["index"] != true {
			t.Errorf("%s.index = %v, want true（不建 HNSW 图则 kNN 查不了）", f, v["index"])
		}
		if v["similarity"] != "cosine" {
			t.Errorf("%s.similarity = %v, want cosine", f, v["similarity"])
		}
	}

	// account 必须是 keyword。写成 text 的话「桶内按租户过滤」会变成分词匹配，
	// A 租户能查到 B 租户的数据——那是越权，不是召回问题。
	if got := objectField(t, props, FieldAccount)["type"]; got != "keyword" {
		t.Errorf("%s.type = %v, want keyword", FieldAccount, got)
	}
	if got := objectField(t, props, FieldChunkId)["type"]; got != "keyword" {
		t.Errorf("%s.type = %v, want keyword", FieldChunkId, got)
	}

	// 三个文本字段走 IK
	for _, f := range []string{FieldTitle, FieldContent, FieldHeadingPath} {
		v := objectField(t, props, f)
		if v["analyzer"] != AnalyzerIKMax {
			t.Errorf("%s.analyzer = %v, want %s", f, v["analyzer"], AnalyzerIKMax)
		}
		if v["search_analyzer"] != AnalyzerIKSmart {
			t.Errorf("%s.search_analyzer = %v, want %s", f, v["search_analyzer"], AnalyzerIKSmart)
		}
	}
}

func TestBuildCreateIndexBody_AllDesignFieldsPresent(t *testing.T) {
	// §4.2 定下的字段一个都不能少：少了某个字段不会报错，
	// 只是查询时静默不参与打分（biz_tag 漏建则 terms 过滤形同虚设）。
	body, err := BuildCreateIndexBody(DefaultIndexSpec())
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}
	props := propsOf(t, body)

	for _, f := range []string{
		FieldAccount, FieldKbId, FieldDocId, FieldChunkId, FieldOrder,
		FieldBizTag,
		FieldTitle, FieldContent, FieldHeadingPath,
		FieldTitleVec, FieldContentVec, FieldCreateTs,
	} {
		if _, ok := props[f]; !ok {
			t.Errorf("字段 %s 缺失", f)
		}
	}
}

func TestBuildCreateIndexBody_RefreshIntervalOmittedWhenEmpty(t *testing.T) {
	spec := DefaultIndexSpec()
	spec.RefreshInterval = ""

	body, err := BuildCreateIndexBody(spec)
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}

	if _, exists := settingsOf(t, body)["refresh_interval"]; exists {
		t.Error("刷新间隔为空时不应写进 body，否则 ES 会当成非法配置")
	}
}

func TestBuildCreateIndexBody_CustomAnalyzerSkipsIKBlock(t *testing.T) {
	// 用非 IK 分词器时不应注入 ik 的 analyzer 定义，否则会声明一个
	// 指向不存在 tokenizer 的 analyzer，建索引直接 400。
	spec := DefaultIndexSpec()
	spec.Analyzer = "standard"
	spec.SearchAnalyzer = "standard"

	body, err := BuildCreateIndexBody(spec)
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}

	if _, exists := settingsOf(t, body)["analysis"]; exists {
		t.Error("非 IK 分词器不应注入 analysis 块")
	}
	if got := objectField(t, propsOf(t, body), FieldContent)["analyzer"]; got != "standard" {
		t.Errorf("content.analyzer = %v, want standard", got)
	}
}

func TestIndexSpec_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*IndexSpec)
		wantErr bool
	}{
		{"默认合法", func(*IndexSpec) {}, false},
		{"维度为 0", func(s *IndexSpec) { s.Dim = 0 }, true},
		{"维度为负", func(s *IndexSpec) { s.Dim = -1 }, true},
		{"维度大到离谱", func(s *IndexSpec) { s.Dim = 8192 }, true},
		{"分词器为空", func(s *IndexSpec) { s.Analyzer = "" }, true},
		{"查询分词器为空", func(s *IndexSpec) { s.SearchAnalyzer = "" }, true},
		{"分片为 0", func(s *IndexSpec) { s.Shards = 0 }, true},
		{"副本为负", func(s *IndexSpec) { s.Replicas = -1 }, true},
		{"副本为 0 合法（单节点）", func(s *IndexSpec) { s.Replicas = 0 }, false},
		{"刷新间隔为空合法", func(s *IndexSpec) { s.RefreshInterval = "" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := DefaultIndexSpec()
			tt.mutate(&spec)

			err := spec.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望报错，实际未报错")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("期望通过，实际报错: %v", err)
			}
		})
	}
}

func TestBuildCreateIndexBody_IsSerializable(t *testing.T) {
	// 这个 body 是要直接 PUT 出去的，必须能序列化——
	// map[string]any 里混进函数或 channel 是运行期才炸的错误
	body, err := BuildCreateIndexBody(DefaultIndexSpec())
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() 报错: %v", err)
	}
	for _, want := range []string{`"dense_vector"`, AnalyzerIKMax, AnalyzerIKSmart} {
		if !strings.Contains(string(b), want) {
			t.Errorf("序列化结果中缺少 %s", want)
		}
	}
}

// TestIndexName_SingleIndex 钉住「单索引多租户」这个决策（设计文档 D2）：
// 全租户共用一个物理索引，没有分桶名 / 独占名 / 版本后缀这些花样。
func TestIndexName_SingleIndex(t *testing.T) {
	if IndexName != "fastrag" {
		t.Errorf("IndexName = %q, want fastrag", IndexName)
	}
}

// TestMapping_IsolationFieldsAreKeyword 钉住租户隔离的两个字段是 keyword：
// account 是唯一的租户隔离手段，写成 text 就是越权（设计文档 §9.2）；
// biz_tag 同理由租户内过滤用，也不能分词。
func TestMapping_IsolationFieldsAreKeyword(t *testing.T) {
	body, err := BuildCreateIndexBody(DefaultIndexSpec())
	if err != nil {
		t.Fatalf("BuildCreateIndexBody() 报错: %v", err)
	}
	props := propsOf(t, body)

	for _, f := range []string{FieldAccount, FieldBizTag, FieldChunkId} {
		p, ok := props[f].(map[string]any)
		if !ok {
			t.Fatalf("mapping 缺少字段 %s", f)
		}
		if got := p["type"]; got != "keyword" {
			t.Errorf("字段 %s 的 type = %v, want keyword", f, got)
		}
	}
}
