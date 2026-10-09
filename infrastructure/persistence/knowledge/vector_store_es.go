// 本文件是 IVectorStore 的 Elasticsearch 实现。
//
// 对应设计文档 §7.2（两路检索）、§9（单索引多租户）。
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/config"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
)

// ESVectorStore IVectorStore 的 Elasticsearch 实现。
type ESVectorStore struct {
	client *elasticsearch.Client
	index  string
	spec   IndexSpec
}

func NewESVectorStore(
	client *elasticsearch.Client, conf *config.Config,
) knowledgerepo.IVectorStore {
	spec := IndexSpec{
		Dim:             conf.Elasticsearch.Dim,
		Analyzer:        conf.Elasticsearch.Analyzer,
		SearchAnalyzer:  conf.Elasticsearch.SearchAnalyzer,
		Shards:          conf.Elasticsearch.Shards,
		Replicas:        conf.Elasticsearch.Replicas,
		RefreshInterval: conf.Elasticsearch.RefreshInterval,
	}

	index := conf.Elasticsearch.Index
	if index == "" {
		index = IndexName
	}

	return &ESVectorStore{client: client, index: index, spec: spec}
}

// EnsureIndex 幂等地确保索引存在。
//
// **只在启动期调用一次**（§9.4，A4.16 的 fx OnStart），写入路径上不碰它。
// 理由见 §9.4：写入路径建索引意味着「没建过索引的集群」和「写过数据的集群」
// 走的是两条不同的代码路径，而后者才是常态——那条路径反而从没被验证过。
//
// 已存在就返回，**不校验 mapping 是否与配置一致**：dense_vector 的 dims
// 建好之后改不了，真出现不一致只能新建索引重导（§9.4、§11 场景 9）。
// 自动「修正」在这里反而危险——它多半会把索引删了重建，等于全量丢数据。
func (s *ESVectorStore) EnsureIndex(ctx context.Context) error {
	res, err := s.client.IndicesExists(
		[]string{s.index},
		s.client.IndicesExists.WithContext(ctx),
	)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case 200:
		return nil
	case 404:
		// 继续往下建
	default:
		return apperrors.NewSysError(apperrors.CodeIndexInitFail, fmt.Sprintf(
			"检查索引失败，ES 返回 %d: %s", res.StatusCode, readBody(res.Body)))
	}

	body, err := BuildCreateIndexBody(s.spec)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}

	create, err := s.client.IndicesCreate(
		s.index,
		s.client.IndicesCreate.WithBody(bytes.NewReader(payload)),
		s.client.IndicesCreate.WithContext(ctx),
	)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}
	defer create.Body.Close()

	if create.IsError() {
		return apperrors.NewSysError(apperrors.CodeIndexInitFail,
			fmt.Sprintf("创建索引失败: %s", readBody(create.Body)))
	}
	return nil
}

// Save 批量写入切片。_id 用 chunk_id（§5.3）。
//
// 纯粹的 upsert：_id 相同就覆盖，不同就新增。**不做任何删除**——
// 清理旧切片是 DeleteExcept 的事（§5.2 ②），两者分开是为了让
// 「新增/覆盖」和「删除」在代码里各自可读、各自可失败。
//
// 覆盖语义之所以成立，靠的是 chunk_id 由内容派生：内容没变 → _id 没变 →
// 覆盖同一篇；内容变了 → _id 变了 → 是新增，旧的那份留给 DeleteExcept 清。
//
// 不用 refresh=true：索引刷新是 30s 级别的（§9），把刷新挂到每次导入上
// 会让写入放大成几十倍。代价是刚导入的文档最多 30s 后才能被搜到。
func (s *ESVectorStore) Save(ctx context.Context, docs []knowledgerepo.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, d := range docs {
		if err := writeJSONLine(&buf, map[string]any{
			"index": map[string]any{"_index": s.index, "_id": d.ChunkID},
		}); err != nil {
			return err
		}
		if err := writeJSONLine(&buf, vectorDocBody(d)); err != nil {
			return err
		}
	}

	res, err := s.client.Bulk(&buf, s.client.Bulk.WithContext(ctx))
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("bulk 请求失败: %s", readBody(res.Body)))
	}

	// bulk 的坑：单条失败时 HTTP 状态码仍然是 200，错误藏在 items 里。
	// 不看 errors 字段的话，导入「成功」了但一半切片根本没进去
	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if !parsed.Errors {
		return nil
	}

	failed := 0
	for _, item := range parsed.Items {
		for _, r := range item {
			if r.Status >= 300 {
				failed++
			}
		}
	}
	return apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
		"bulk 部分失败: %d/%d 条", failed, len(docs)))
}

// Refresh 强制刷新索引，让刚写入的切片立刻进入可检索视图。
//
// 它存在是因为 ES 的可见性有两档延迟，而这两档都会咬到我们（§5.2 ②）：
//
//   - **新写入的**要等下一次刷新才可见（默认 30s，§4.2）；
//   - **delete_by_query 也只看得到已刷新的段**——所以「上一批还没刷出来的旧切片」
//     对删除动作是**不存在**的，删了等于没删。
//
// 调用方在两种情况下需要它：重导入（要让 ② 看得见上一批旧切片）、
// 以及调用方要求「导入即可搜」（§4.2）。
//
// ⚠️ 粒度是**整个索引**：ES 没有「按文档刷新」这种操作。单索引多租户（D2）下
// 会顺带刷到别的租户的段，所以只在上面两种情况下调，别放进常规写入路径。
func (s *ESVectorStore) Refresh(ctx context.Context) error {
	res, err := s.client.IndicesRefresh(
		s.client.IndicesRefresh.WithContext(ctx),
		s.client.IndicesRefresh.WithIndex(s.index),
	)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("刷新索引失败: %s", readBody(res.Body)))
	}
	return nil
}

// DeleteByQuery 按条件删除。
func (s *ESVectorStore) DeleteByQuery(
	ctx context.Context, f knowledgerepo.VectorFilter,
) (int64, error) {
	query, ok := buildFilter(f)
	if !ok {
		// 三个条件全空 = 删全库。这几乎一定是调用方漏传了参数，
		// 拒绝掉。宁可报错，也不能把一个索引清空
		return 0, apperrors.NewParamError("删除条件不能全为空")
	}

	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}

	res, err := s.client.DeleteByQuery(
		[]string{s.index},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		// 删到一半撞上版本冲突时继续，别把整批回滚
		s.client.DeleteByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("delete_by_query 失败: %s", readBody(res.Body)))
	}

	// 同样注意：delete_by_query 可能返回 200 但 failures 非空
	var parsed struct {
		Deleted  int64 `json:"deleted"`
		Failures []struct {
			Reason struct {
				Reason string `json:"reason"`
			} `json:"reason"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if len(parsed.Failures) > 0 {
		return parsed.Deleted, apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
			"delete_by_query 有 %d 条失败，首条: %s",
			len(parsed.Failures), parsed.Failures[0].Reason.Reason))
	}
	return parsed.Deleted, nil
}

// DeleteExcept 删除过滤范围内、_id 不在 keepIDs 里的切片（§5.2 第 ② 步）。
//
// 这是「先写后删」里的那个"删"。重导入时同一篇文档的切片 _id 会因为内容变化
// 而变（chunk_id = sha256(doc_id, order, content)），所以旧切片不会被新写入
// 覆盖，必须显式清理——删的正是「旧集合减去新集合」这个差集。
//
// 为什么按 _id 差集删、而不是 DeleteByQuery(doc_id) 一把删光：
//   - 按 doc_id 删光会把**内容没变的那批切片**也删掉。它们本来 _id 相同、
//     刚刚被 Save 原样覆盖过一遍，删掉再写一次纯属写入放大；
//   - 更糟的是顺序：Save 在前、删在后，中间这一小段时间内容没变的切片
//     是"写完又被删"——如果删除请求刚好在 Save 之后、下一次导入之前完成，
//     这篇文档就凭空少了几个切片。
//
// 与 DeleteByQuery 分开是有意的：这两个的杀伤面差一个数量级。
// DeleteByQuery 是「删掉命中的全部」，DeleteExcept 是「只删差集」。
// 靠一个空 keepIDs 参数区分（传空 = 删全部），早晚有人传空把整篇文档清空。
//
// 单独开一个方法还有个好处：doc_id 的条件写在这里，调用方没有机会忘。
//
// ⚠️ 已知缺口（§5.2、§11）：Save 之后、DeleteExcept 生效之前，新旧切片共存。
// refresh_interval=30s 时这个窗口可以持续一个刷新周期。期间检索可能重复召回
// 同一段内容的两个版本。本期接受——写侧一致性（代次切换）是明确推迟的增强项（§13 待定 9）。
func (s *ESVectorStore) DeleteExcept(
	ctx context.Context, f knowledgerepo.VectorFilter, keepIDs []string,
) (int64, error) {
	if f.DocID == 0 {
		// 没有 doc_id 就没有「这篇文档的旧切片」可言。
		// 放行等于按 kb_id 删掉一整个库的切片，必须拦下
		return 0, apperrors.NewParamError("DeleteExcept 必须指定 doc_id")
	}

	base, ok := buildFilter(f)
	if !ok {
		return 0, apperrors.NewParamError("删除条件不能全为空")
	}

	// 在已有条件上再 AND 一个 must_not(_id in keepIDs)。
	// 一定要包进 bool.must：buildFilter 返回的本身就是一个 bool 查询，
	// 直接把 must_not 和它并列会变成「或」的关系——那会把刚写的新切片也删掉。
	inner := base["bool"].(map[string]any)
	must := inner["must"].([]any)
	boolQuery := map[string]any{"must": must}

	// keepIDs 为空时不加 must_not —— terms 查询传空数组会命中 0 条文档，
	// 那样 DeleteExcept 就成了 no-op，旧切片永远清不掉。
	// 空集合的正确语义是"没有要留的"，即删掉全部命中，正好是不加 must_not。
	if len(keepIDs) > 0 {
		boolQuery["must_not"] = []any{
			map[string]any{"terms": map[string]any{"_id": keepIDs}},
		}
	}

	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"bool": boolQuery},
	})
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}

	res, err := s.client.DeleteByQuery(
		[]string{s.index},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		s.client.DeleteByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("delete_except 失败: %s", readBody(res.Body)))
	}

	// 同 DeleteByQuery：200 也可能带 failures
	var parsed struct {
		Deleted  int64 `json:"deleted"`
		Failures []struct {
			Reason struct {
				Reason string `json:"reason"`
			} `json:"reason"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if len(parsed.Failures) > 0 {
		return parsed.Deleted, apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
			"delete_except 有 %d 条失败，首条: %s",
			len(parsed.Failures), parsed.Failures[0].Reason.Reason))
	}
	return parsed.Deleted, nil
}

// Search 发 BM25 + kNN 两路，原样返回、不融合。
func (s *ESVectorStore) Search(
	ctx context.Context, req knowledgerepo.VectorSearchReq,
) (knowledgerepo.SearchResult, error) {
	var out knowledgerepo.SearchResult

	type route struct {
		name string
		body map[string]any
		dst  *[]knowledgerepo.VectorHit
	}

	routes := make([]route, 0, 2)
	if req.DenseWeight <= 0.99 {
		routes = append(routes, route{"bm25", s.bm25Body(req), &out.BM25})
	}
	if req.DenseWeight >= 0.01 && len(req.QueryVec) > 0 {
		routes = append(routes, route{"knn", s.knnBody(req), &out.KNN})
	}

	// 两路并行发：ES 扛得住这个并发，串行只会让 P99 白白翻倍
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, rt := range routes {
		wg.Add(1)
		go func(rt route) {
			defer wg.Done()

			hits, err := s.runSearch(ctx, rt.body)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rt.name, err))
				return
			}
			*rt.dst = hits
		}(rt)
	}
	wg.Wait()

	if len(errs) > 0 {
		return out, errors.Join(errs...)
	}
	return out, nil
}

func (s *ESVectorStore) runSearch(
	ctx context.Context, body map[string]any,
) ([]knowledgerepo.VectorHit, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.index),
		s.client.Search.WithBody(bytes.NewReader(payload)),
	)
	if err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, apperrors.NewSysError(apperrors.CodeSearchFail,
			fmt.Sprintf("检索失败，ES 返回 %d: %s", res.StatusCode, readBody(res.Body)))
	}

	var parsed struct {
		Hits struct {
			Hits []struct {
				Score  float64         `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}

	out := make([]knowledgerepo.VectorHit, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		var src struct {
			ChunkID     string `json:"chunk_id"`
			DocID       uint64 `json:"doc_id"`
			KBID        uint64 `json:"kb_id"`
			Order       int    `json:"order"`
			Title       string `json:"title"`
			Content     string `json:"content"`
			HeadingPath string `json:"heading_path"`
		}
		if err := json.Unmarshal(h.Source, &src); err != nil {
			// 单条解析失败不该让整次检索挂掉
			continue
		}
		out = append(out, knowledgerepo.VectorHit{
			ChunkID:     src.ChunkID,
			DocID:       src.DocID,
			KBID:        src.KBID,
			Order:       src.Order,
			Title:       src.Title,
			Content:     src.Content,
			HeadingPath: src.HeadingPath,
			Score:       h.Score,
		})
	}
	return out, nil
}

// bm25Body 关键词路。
func (s *ESVectorStore) bm25Body(req knowledgerepo.VectorSearchReq) map[string]any {
	// boost 的取值：标题命中比正文命中强得多——一份文档里正文到处都是
	// 常见词，标题才真正说明这篇在讲什么
	fields := []string{FieldTitle + "^3", FieldHeadingPath + "^2", FieldContent}
	if req.TitleOnly {
		// search_mode = title：不查正文
		fields = []string{FieldTitle + "^3", FieldHeadingPath + "^2"}
	}

	should := make([]any, 0, len(fields))
	for _, f := range fields {
		should = append(should, map[string]any{
			"match": map[string]any{f: map[string]any{"query": req.Query}},
		})
	}

	return map[string]any{
		"size": req.BM25Top,
		"query": map[string]any{
			"bool": map[string]any{
				// filter 里的条件不参与打分，只做范围限定。
				// 租户隔离必须走这里——放进 must 的话，
				// 一个 account 的匹配会把 BM25 分数整体抬高
				"filter":               s.filters(req),
				"should":               should,
				"minimum_should_match": 1,
			},
		},
		"_source": sourceFields(),
	}
}

// knnBody 向量路。
func (s *ESVectorStore) knnBody(req knowledgerepo.VectorSearchReq) map[string]any {
	field := FieldContentVec
	if req.TitleOnly {
		field = FieldTitleVec
	}

	k := req.KNNTops
	numCandidates := req.NumCandidates
	if numCandidates < k {
		// num_candidates 必须 >= k，否则 ES 直接 400
		numCandidates = k
	}

	return map[string]any{
		"size": k,
		"knn": map[string]any{
			"field":          field,
			"query_vector":   req.QueryVec,
			"k":              k,
			"num_candidates": numCandidates,
			"filter":         s.filters(req),
		},
		"_source": sourceFields(),
	}
}

// filters 所有「只做范围限定、不参与打分」的条件。
//
// account 必须在最前，且必须用 term：它是单索引下唯一的租户隔离手段（D2）。
// 写成 match 会按分词匹配，A 租户能查到 B 租户的数据——那是越权，不是召回问题。
func (s *ESVectorStore) filters(req knowledgerepo.VectorSearchReq) []any {
	out := []any{
		map[string]any{"term": map[string]any{FieldAccount: req.Account}},
	}
	if len(req.KBIDs) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{FieldKbId: req.KBIDs}})
	}
	if len(req.BizTags) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{FieldBizTag: req.BizTags}})
	}
	return out
}

// buildFilter 把 VectorFilter 翻成 ES 查询。返回 false 表示条件全空。
func buildFilter(f knowledgerepo.VectorFilter) (map[string]any, bool) {
	must := make([]any, 0, 3)

	if f.Account != "" {
		must = append(must, map[string]any{"term": map[string]any{FieldAccount: f.Account}})
	}
	if f.KBID != 0 {
		must = append(must, map[string]any{"term": map[string]any{FieldKbId: f.KBID}})
	}
	if f.DocID != 0 {
		must = append(must, map[string]any{"term": map[string]any{FieldDocId: f.DocID}})
	}

	if len(must) == 0 {
		return nil, false
	}
	return map[string]any{"bool": map[string]any{"must": must}}, true
}

func sourceFields() []string {
	return []string{
		FieldChunkId, FieldDocId, FieldKbId, FieldOrder,
		FieldTitle, FieldContent, FieldHeadingPath,
	}
}

func vectorDocBody(d knowledgerepo.VectorDoc) map[string]any {
	return map[string]any{
		FieldAccount:     d.Account,
		FieldKbId:        d.KBID,
		FieldDocId:       d.DocID,
		FieldChunkId:     d.ChunkID,
		FieldOrder:       d.Order,
		FieldBizTag:      d.BizTag,
		FieldTitle:       d.Title,
		FieldContent:     d.Content,
		FieldHeadingPath: d.HeadingPath,
		FieldTitleVec:    d.TitleVec,
		FieldContentVec:  d.ContentVec,
		FieldCreateTs:    d.CreateTs,
	}
}

func writeJSONLine(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	buf.Write(b)
	buf.WriteByte('\n')
	return nil
}

// readBody 读掉响应体并截断。
// ES 的错误体可以很长，原样塞进错误信息会把日志冲爆。
func readBody(r io.Reader) string {
	const maxLen = 512

	b, _ := io.ReadAll(io.LimitReader(r, maxLen*4))
	// 按 rune 截断：按字节截会把一个中文切成两半，日志里就是乱码
	runes := []rune(strings.TrimSpace(string(b)))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "...(截断)"
}
