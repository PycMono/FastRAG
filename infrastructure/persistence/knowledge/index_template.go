// 本文件提供 FastRAG 的 Elasticsearch 索引定义（IVectorStore 的 ES 实现的附属物）。
//
// 只负责「索引长什么样」——mapping / settings / 命名规则，
// 不依赖任何 ES 客户端，所以任何 HTTP 客户端、任何版本的 ES 都能用。
// 需要落到集群时，把 BuildCreateIndexBody 的返回值直接 PUT 到 /{index} 即可。
//
// 对应设计文档 §4.2（mapping）与 §9（单索引多租户）。
package knowledge

import (
	"fmt"

	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// 字段名常量。ES 的字段名散落在查询体与 mapping 两处，
// 写错一个字母不会报错，只会静默查不到结果，所以统一收口到这里。
const (
	FieldAccount     = "account"      // 租户，keyword —— 单索引下这是唯一的租户隔离手段
	FieldKbId        = "kb_id"        // 知识库 ID，long
	FieldDocId       = "doc_id"       // 文档 ID（雪花），long
	FieldChunkId     = "chunk_id"     // 切片 ID，keyword，也是 _id 的来源
	FieldOrder       = "order"        // 切片在文档内的顺序，integer
	FieldBizTag      = "biz_tag"      // 业务标签，租户内再分域的过滤，keyword
	FieldTitle       = "title"        // 切片标题，text，IK 分词
	FieldContent     = "content"      // 切片正文，text，IK 分词
	FieldHeadingPath = "heading_path" // 标题链，text —— 结构感知切片产生，相当于廉价版的 contextual retrieval
	FieldTitleVec    = "title_vec"    // 标题向量
	FieldContentVec  = "content_vec"  // 正文向量
	FieldCreateTs    = "create_ts"    // 毫秒时间戳，long
)

// 默认分词器。装了 analysis-ik 插件才有；没装的话建索引会直接报
// `analyzer [ik_max_word] not found`——属于「早失败」，比静默退化成按字切好。
const (
	AnalyzerIKMax   = "ik_max_word" // 索引用：穷尽切分，召回高
	AnalyzerIKSmart = "ik_smart"    // 查询用：粗粒度切分，精度高
)

// 索引命名。
//
// 单索引多租户（设计文档 D2）：所有租户共用一个物理索引，隔离靠 account 字段。
// 不做「一租户一索引」（SaaS 租户数一多就炸），也不做分桶/独占（本期不需要）。
const IndexName = "fastrag"

// IndexSpec 索引规格。
//
// Dim 和分词器都做成参数而不是写死，是因为它们属于「部署环境的事实」：
// 向量维度由选定的 embedding 模型决定，分词器由装了哪个插件决定。
// 写死会导致换模型时要改代码，而不是改配置。
type IndexSpec struct {
	Dim             int    // 向量维度，必须与 embedding 模型一致
	Analyzer        string // 建索引时的分词器
	SearchAnalyzer  string // 查询时的分词器
	Shards          int    // 分片数
	Replicas        int    // 副本数
	RefreshInterval string // 刷新间隔，如 "30s"；"1s" 会显著拖慢写入
}

// DefaultIndexSpec 返回本地开发/起步阶段的默认规格。
//
// ⚠️ 运行期**不使用**这个函数：ESVectorStore 的 spec 是从 config.json 的
// elasticsearch 块构造的（A4.6）。留在这里是给单测和「手工建索引」用的——
// 配置漏填时，TestBuildCreateIndexBody_Defaults 会先红，而不是等
// 部署到环境上才发现维度不对。
//
// 关于 Shards=3：单索引下分片数要按**全量**语料估（不是按租户），
// 生产上应随数据量调整（设计文档 §9.4）。本地起步 3 片够用。
//
// 关于 Dim=1024：这是**占位值**，必须与最终选定的 embedding 模型对齐
// （设计文档 §13 的待定项）。改维度等于改 mapping，dense_vector 的 dims 不可变，
// 只能新建索引重导——本期没有影索引机制，代价是停写重导。
func DefaultIndexSpec() IndexSpec {
	return IndexSpec{
		Dim:             1024,
		Analyzer:        AnalyzerIKMax,
		SearchAnalyzer:  AnalyzerIKSmart,
		Shards:          3,
		Replicas:        1,
		RefreshInterval: "30s",
	}
}

// Validate 校验规格，把错误拦在建索引之前。
//
// 全部按系统错误处理：这些值来自配置，填错了是部署事故，
// 不是用户输入错误，不该以业务错误的形态透出给调用方。
func (s IndexSpec) Validate() error {
	bad := func(format string, args ...any) error {
		return apperrors.NewSysError(apperrors.CodeInternal, "es: "+fmt.Sprintf(format, args...))
	}

	switch {
	case s.Dim <= 0:
		return bad("Dim 必须为正数，当前 %d", s.Dim)
	case s.Dim > 4096:
		// ES 对 dense_vector 维度没有硬上限，但超过 4096 后 HNSW 的
		// 收益远小于内存代价，基本可以断定是填错了
		return bad("Dim=%d 过大，疑似配置错误", s.Dim)
	case s.Analyzer == "" || s.SearchAnalyzer == "":
		return bad("Analyzer / SearchAnalyzer 不能为空")
	case s.Shards <= 0:
		return bad("Shards 必须为正数，当前 %d", s.Shards)
	case s.Replicas < 0:
		return bad("Replicas 不能为负数，当前 %d", s.Replicas)
	}
	return nil
}

// BuildCreateIndexBody 构造 PUT /{index} 的请求体。
//
// 返回 map 而非字符串，是为了让调用方能自由地再塞 settings
// （比如加 index.max_result_window）或 properties 而不用做 JSON 反序列化。
func BuildCreateIndexBody(spec IndexSpec) (map[string]any, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	settings := map[string]any{
		"number_of_shards":   spec.Shards,
		"number_of_replicas": spec.Replicas,
	}

	// 空字符串是合法的 ES 配置（表示用默认刷新策略），此时不要写进 body，
	// 否则会被当成非法值
	if spec.RefreshInterval != "" {
		settings["refresh_interval"] = spec.RefreshInterval
	}

	// 自定义分析器：把 tokenizer 包一层再暴露成 analyzer 名。
	// 这么做是为了让 mapping 里写 ik_max_word / ik_smart，而不是到处
	// 写 "analyzer": {"type": "custom", "tokenizer": ...} 那坨。
	//
	// 注意：如果集群装了 analysis-ik，它本身就提供了同名的内置
	// analyzer，这里重复定义会以我们这份为准（等价），无害。
	if spec.Analyzer == AnalyzerIKMax {
		settings["analysis"] = map[string]any{
			"analyzer": map[string]any{
				AnalyzerIKMax: map[string]any{
					"type":      "custom",
					"tokenizer": AnalyzerIKMax,
				},
				AnalyzerIKSmart: map[string]any{
					"type":      "custom",
					"tokenizer": AnalyzerIKSmart,
				},
			},
		}
	}

	textField := func() map[string]any {
		return map[string]any{
			"type":            "text",
			"analyzer":        spec.Analyzer,
			"search_analyzer": spec.SearchAnalyzer,
		}
	}
	vectorField := func() map[string]any {
		return map[string]any{
			"type":       "dense_vector",
			"dims":       spec.Dim,
			"index":      true,     // 建 HNSW 图；false 就只能当存储用，kNN 查不了
			"similarity": "cosine", // embedding 模型基本都按余弦相似度训练
			// 不显式给 index_options，ES 9 会默认用 bbq_hnsw（二值量化 HNSW）。
			// 好处是图的内存占用降到约 1/4，代价是召回略有损失——
			// ES 用 rescore_vector.oversample=3.0 补回来一部分。
			// 若实测召回不达标，再加 index_options 覆盖成纯 hnsw：
			//   "index_options": {"type": "hnsw", "m": 16, "ef_construction": 100}
		}
	}

	properties := map[string]any{
		FieldAccount: map[string]any{"type": "keyword"}, // keyword 而非 text —— 租户隔离靠它，不能分词
		FieldKbId:    map[string]any{"type": "long"},
		FieldDocId:   map[string]any{"type": "long"},
		FieldChunkId: map[string]any{"type": "keyword"}, // 主键语义，别分词
		FieldOrder:   map[string]any{"type": "integer"},
		FieldBizTag:  map[string]any{"type": "keyword"}, // 租户内再分域，过滤用

		FieldTitle:       textField(),
		FieldContent:     textField(),
		FieldHeadingPath: textField(),

		FieldTitleVec:   vectorField(),
		FieldContentVec: vectorField(),

		FieldCreateTs: map[string]any{"type": "long"},
	}

	return map[string]any{
		"settings": settings,
		"mappings": map[string]any{
			"properties": properties,
		},
	}, nil
}
