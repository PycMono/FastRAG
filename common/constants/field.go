// 本文件定义 ES 的字段名常量与索引名 —— 查询体与写入体的共同词汇表。
//
// ⚠️ 索引**不由本服务创建**（设计文档 §9.4）。mapping / settings 的权威版本在
// scripts/create-es-index.sh，本文件只是它的另一半：脚本定「字段长什么样」，
// 这里定「代码里怎么称呼它们」。
//
//	改了一边的字段名，就要改另一边。两边对不上不会报错，
//	只会静默查不到结果 —— 这是本设计里最容易踩的漂移点。
//
// 对应设计文档 §4.2（mapping）与 §9（单索引多租户）。

package constants

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

// IndexName 索引命名。
//
// 单索引多租户（设计文档 D2）：所有租户共用一个物理索引，隔离靠 account 字段。
// 不做「一租户一索引」（SaaS 租户数一多就炸），也不做分桶/独占（本期不需要）。
//
// 配置里 elasticsearch.index 留空时兜底用它。
const IndexName = "fastrag"

// 索引名、维度、分词器、分片/副本、刷新间隔都曾经是这里的 IndexSpec ——
// 已全部搬到 scripts/create-es-index.sh。运行期不再需要它们：
// 服务只读写索引，不建索引。
