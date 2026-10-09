// 本文件是 IVectorStore 的 Elasticsearch 实现。
//
// 对应设计文档 §7.2（两路检索）、§9（单索引多租户）。
package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/infrastructure/config"
	logsdk "github.com/PycMono/go-logger-sdk"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
)

// ESVectorStore IVectorStore 的 Elasticsearch 实现。
type ESVectorStore struct {
	client *elasticsearch.Client
	index  string

	// indexChecked 为真表示「已经确认过线上索引存在且可用」。
	// 见 checkIndex：只在第一次用到时校一次，之后是一次原子读，不进热路径。
	indexChecked atomic.Bool
}

func NewESVectorStore(
	client *elasticsearch.Client, conf *config.Config,
) repository.IVectorStore {
	index := conf.ES.Index
	if index == "" {
		index = constants.IndexName
	}

	return &ESVectorStore{client: client, index: index}
}

// esField 是 mapping 里一个字段的最小投影，冒烟检查够用。
type esField struct {
	Type string `json:"type"`
	Dims int    `json:"dims"`
}

// checkIndex 确认线上索引存在、且是「本服务要的那个」索引。
//
// 为什么需要它：建索引已经不在本服务职责内（§9.4），索引成了**外部前提**。
// 前提不成立时，最糟的结果不是报错而是静默 —— ES 的 action.auto_create_index
// 默认是 true，一次写入就能让 ES 自己建出一个动态 mapping 的索引
// （无 IK 分词、无 dense_vector、account 变 text），而 bulk 返回成功、毫无提示；
// 等检索时才以「字段不存在」这类面目全非的错误浮现出来。
//
// 用户明确要求**不在启动期**校验（ES 没准备好不该让整个服务起不来），
// 所以这里是「第一次用时报清楚」。
//
// 只查一次：成功后置位，之后每个请求只付一次原子读。**失败不置位** ——
// 运维补建完索引，下一次请求自动恢复，不必重启服务。
func (s *ESVectorStore) checkIndex(ctx context.Context) error {
	if s.indexChecked.Load() {
		return nil
	}

	res, err := s.client.Indices.GetMapping(
		s.client.Indices.GetMapping.WithContext(ctx),
		s.client.Indices.GetMapping.WithIndex(s.index),
	)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	// ⚠️ 响应体只能读一次。而且不能走 readBody —— 它截断到 512 rune，
	// 拿来喂 json.Unmarshal 会解析失败。整份读进来，报错时再截断。
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	raw := string(body)

	if res.StatusCode == http.StatusNotFound {
		return s.errIndexMissing()
	}
	if res.IsError() {
		return apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("检查索引失败，ES 返回 %d: %s", res.StatusCode, truncateForLog(raw)))
	}

	var parsed map[string]struct {
		Mappings struct {
			Properties map[string]esField `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}

	if bad := smokeCheckMapping(parsed[s.index].Mappings.Properties); bad != "" {
		return apperrors.NewSysError(apperrors.CodeIndexInitFail, fmt.Sprintf(
			"ES 索引 %q 不像本服务要的索引：%s。\n"+
				"多半是 ES 按动态 mapping 自己建出来的（action.auto_create_index 默认为 true）。\n"+
				"本服务不创建索引，请核对后重建：\n  bash scripts/create-es-index.sh",
			s.index, bad))
	}

	s.indexChecked.Store(true)
	return nil
}

// errIndexMissing 是索引不存在时给调用方看的话 —— 要能照着做，不能只说「失败了」。
func (s *ESVectorStore) errIndexMissing() error {
	return apperrors.NewSysError(apperrors.CodeIndexInitFail, fmt.Sprintf(
		"ES 索引 %q 不存在。索引不由本服务创建，请先执行：\n  bash scripts/create-es-index.sh",
		s.index))
}

// smokeCheckMapping 只是冒烟，**不是** mapping 的权威校验 ——
// 权威版本在 scripts/create-es-index.sh，那里逐字段比对。
// 这里只挑两个「类型本身就是语义」的字段：
//
//   - account 必须是 keyword：单索引多租户下这是唯一的隔离手段（D2）。
//     若被建成 text，term 查询会按分词匹配，「demo」就能命中「demo-other」的文档 ——
//     那是**跨租户越权**，不是召回变小。
//   - content_vec 必须是 dense_vector：否则 kNN 那一路直接不可用。
//
// 动态 mapping 建出来的索引，两条都不过。
func smokeCheckMapping(props map[string]esField) string {
	var bad []string
	if f, ok := props[constants.FieldAccount]; !ok {
		bad = append(bad, fmt.Sprintf("缺字段 %s", constants.FieldAccount))
	} else if f.Type != "keyword" {
		bad = append(bad, fmt.Sprintf("%s 是 %s，应为 keyword（租户隔离靠它）", constants.FieldAccount, f.Type))
	}
	if f, ok := props[constants.FieldContentVec]; !ok {
		bad = append(bad, fmt.Sprintf("缺字段 %s", constants.FieldContentVec))
	} else if f.Type != "dense_vector" {
		bad = append(bad, fmt.Sprintf("%s 是 %s，应为 dense_vector（kNN 检索靠它）", constants.FieldContentVec, f.Type))
	}
	return strings.Join(bad, "；")
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
func (s *ESVectorStore) Save(ctx context.Context, docs []repository.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}

	// 索引是外部前提（§9.4）。**必须写之前查** —— 写完再查就晚了：
	// ES 的 auto_create_index 会在这条 bulk 里悄悄把索引建错，而且返回成功。
	if err := s.checkIndex(ctx); err != nil {
		return err
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
	res, err := s.client.Indices.Refresh(
		s.client.Indices.Refresh.WithContext(ctx),
		s.client.Indices.Refresh.WithIndex(s.index),
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
	ctx context.Context, f repository.VectorFilter,
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
	ctx context.Context, f repository.VectorFilter, keepIDs []string,
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
		// 删到一半撞上版本冲突时继续，别把整批回滚
		s.client.DeleteByQuery.WithConflicts("proceed"),
		// refresh 必须开（§5.2 ②）：删除和写入一样，**要等下一次刷新才对检索可见**。
		// 而调用方的刷新在删除之前——那是为了「让 delete_by_query 看得见上一批旧切片」，
		// 帮不到这里的「让删除结果被检索看见」。少了这一次刷新，
		// 重导入之后立刻检索会同时返回新旧两份内容，直到索引的 30s 周期刷新到来，
		// §12.1 那条「重导入后旧切片搜不到」就会时灵时不灵，且窗口正好落在
		// 「刚导入完就去搜」这个最自然的用法上。
		//
		// 语义上也不该省：调用方请求的是「写完即可检索」，而「写完」包含清掉旧的。
		s.client.DeleteByQuery.WithRefresh(true),
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
	ctx context.Context, req repository.VectorSearchReq,
) (repository.SearchResult, error) {
	var out repository.SearchResult

	type route struct {
		name string
		body map[string]any
		dst  *[]repository.VectorHit
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

			s.logSearch(ctx, rt.name, rt.body)

			hits, err := s.runSearch(ctx, rt.body)

			// 在加锁前记，别让日志把两路的并发串起来
			s.logSearchResult(ctx, rt.name, hits, err)

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

// logSearch 把真正发给 ES 的请求体打出来，每次检索两条（route=bm25 / route=knn）。
//
// 存在的理由：混合检索一次发**两个**请求，只看其中一个很容易得出「根本没走向量检索」
// 的错误结论——2026-10-09 就是这么误判的。这里把两路都打出来，靠 route 字段区分。
//
// 想看 ES 那一侧收到的原始报文，那是另一条路（ES 慢日志），不要和这里混为一谈。
func (s *ESVectorStore) logSearch(ctx context.Context, route string, body map[string]any) {
	logsdk.Info(ctx, "检索请求发往 ES",
		logsdk.Any("route", route),
		logsdk.Any("index", s.index),
		logsdk.Any("body", summarizeQueryVec(body)),
	)
}

// logSearchResult 记每一路**回来了什么**。
//
// 和 logSearch 配对用：那个证明「发出去了」，这个证明「回来了」。
// 只有前者的话，日志里看到 route=knn 也仍然不知道向量路到底召回没有——
// 2026-10-09 就被问过一次「是不是真查到数据了」。
//
// top_score 对向量路是**各条 kNN 子句余弦相似度之和**，content 模式下有
// 标题+正文两条子句，所以量程是 0~2 而不是 0~1；title 模式只有一条，是 0~1。
// 对 BM25 路则是无上界的相关性分。三者的数不能横向比。
func (s *ESVectorStore) logSearchResult(
	ctx context.Context, route string, hits []repository.VectorHit, err error,
) {
	if err != nil {
		logsdk.Error(ctx, "ES 检索返回失败",
			logsdk.Any("route", route),
			logsdk.Err(err),
		)
		return
	}

	fields := []logsdk.Fields{
		logsdk.Any("route", route),
		logsdk.Any("hits", len(hits)),
	}
	// 空结果是合法状态（这个租户没数据、或过滤后为空），不当错误报，
	// 但 hits=0 本身就是要看的信号，所以照记。
	if len(hits) > 0 {
		fields = append(fields,
			logsdk.Any("top_chunk_id", hits[0].ChunkID),
			logsdk.Any("top_score", hits[0].Score),
		)
	}
	logsdk.Info(ctx, "ES 检索返回", fields...)
}

// summarizeQueryVec 返回 body 的浅拷贝，把 knn 里的 query_vector 换成一句摘要。
//
// 1024 个 float32 原样打出来会把日志刷爆，而排查「有没有走向量路」只需要知道
// 维度对不对、数值是不是全零。原 body 要原样发给 ES，所以只能拷贝不能改。
//
// knn 有两种形态都要认：**数组**（content 模式，标题+正文两条子句）和
// **单对象**（title 模式一条）。只认其中一种的话，另一种形态会在日志里
// 原样吐出 1024 个数——而日志恰恰是排查这类问题时唯一能看的东西。
func summarizeQueryVec(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}

	switch knn := out["knn"].(type) {
	case map[string]any:
		out["knn"] = summarizeClause(knn)
	case []any:
		clauses := make([]any, 0, len(knn))
		for _, c := range knn {
			m, ok := c.(map[string]any)
			if !ok {
				clauses = append(clauses, c)
				continue
			}
			clauses = append(clauses, summarizeClause(m))
		}
		out["knn"] = clauses
	}
	return out
}

// summarizeClause 把一条 knn 子句的 query_vector 换成摘要，其余字段原样。
func summarizeClause(clause map[string]any) map[string]any {
	cp := make(map[string]any, len(clause))
	for k, v := range clause {
		cp[k] = v
	}

	if vec, ok := clause["query_vector"].([]float32); ok {
		cp["query_vector"] = describeVec(vec)
	}
	return cp
}

func describeVec(vec []float32) string {
	if len(vec) < 8 {
		return fmt.Sprintf("<%d 维 float32: %v>", len(vec), vec)
	}
	return fmt.Sprintf("<%d 维 float32，首尾各 4 个: %.6f %.6f %.6f %.6f ... %.6f %.6f %.6f %.6f>",
		len(vec),
		vec[0], vec[1], vec[2], vec[3],
		vec[len(vec)-4], vec[len(vec)-3], vec[len(vec)-2], vec[len(vec)-1],
	)
}

func (s *ESVectorStore) runSearch(
	ctx context.Context, body map[string]any,
) ([]repository.VectorHit, error) {
	// 这里是 Search 两条路（BM25 / kNN）唯一的汇聚点，所以校验放这儿，
	// 而不是放在 Search 那把两个 error 用 errors.Join 拼起来的地方 ——
	// 从拼好的字符串里认 404 太脆。
	if err := s.checkIndex(ctx); err != nil {
		return nil, err
	}

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

	out := make([]repository.VectorHit, 0, len(parsed.Hits.Hits))
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
		out = append(out, repository.VectorHit{
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
func (s *ESVectorStore) bm25Body(req repository.VectorSearchReq) map[string]any {
	// boost 的取值：标题命中比正文命中强得多——一份文档里正文到处都是
	// 常见词，标题才真正说明这篇在讲什么
	fields := []string{constants.FieldTitle + "^3", constants.FieldHeadingPath + "^2", constants.FieldContent}
	if req.TitleOnly {
		// search_mode = title：不查正文
		fields = []string{constants.FieldTitle + "^3", constants.FieldHeadingPath + "^2"}
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
//
// knn 这里传的是**数组**：content 模式下标题向量和正文向量各出一条子句，
// ES 把两条子句的分数**相加**后出一个榜单（实测 0.969 + 0.840 = 1.809）。
//
// 为什么要两条子句：只查 content_vec 时，「标题讲这件事、正文恰好没重复这个词」
// 的切片永远进不了榜。2026-10-09 在同一批数据上实测，加上 title_vec 后 top50 里
// 有 18 条是单查 content_vec 召不回来的（交集 32 条）。
//
// 对齐的是 go-ai-knowledge 的做法——它在 ES 原生 RRF 里给 title_vec / content_vec
// 各挂一个 retriever。区别只在融合口径：它是按**名次**融合，这里是按**分数**相加。
// 相加的语义是「两处都像」压过「一处极像」，也正是 ES 官方对多字段 kNN 的推荐用法。
//
// title 模式（search_mode = title）仍然只发一条：那条路的语义就是「不碰正文」
// （§7.2），顺带查 content_vec 等于把只按标题建的库悄悄按正文检索。
func (s *ESVectorStore) knnBody(req repository.VectorSearchReq) map[string]any {
	return map[string]any{
		"size":    req.KNNTops,
		"knn":     s.knnClauses(req),
		"_source": sourceFields(),
	}
}

// knnClauses 按检索模式决定查几个向量字段，逐条构造 kNN 子句。
func (s *ESVectorStore) knnClauses(req repository.VectorSearchReq) []any {
	fields := []string{constants.FieldTitleVec, constants.FieldContentVec}
	if req.TitleOnly {
		fields = []string{constants.FieldTitleVec}
	}

	k := req.KNNTops
	numCandidates := req.NumCandidates
	if numCandidates < k {
		// num_candidates 必须 >= k，否则 ES 直接 400。
		// 每条子句各自受这条约束，所以放在循环外算一次
		numCandidates = k
	}

	out := make([]any, 0, len(fields))
	for _, f := range fields {
		out = append(out, map[string]any{
			"field":          f,
			"query_vector":   req.QueryVec,
			"k":              k,
			"num_candidates": numCandidates,
			// filter 必须挂在**每条子句**上，不能只挂请求外层。
			// 漏掉一条 = 那条子句跨租户召回，account 隔离就只剩一半（D2）
			"filter": s.filters(req),
		})
	}
	return out
}

// filters 所有「只做范围限定、不参与打分」的条件。
//
// account 必须在最前，且必须用 term：它是单索引下唯一的租户隔离手段（D2）。
// 写成 match 会按分词匹配，A 租户能查到 B 租户的数据——那是越权，不是召回问题。
func (s *ESVectorStore) filters(req repository.VectorSearchReq) []any {
	out := []any{
		map[string]any{"term": map[string]any{constants.FieldAccount: req.Account}},
	}
	if len(req.KBIDs) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{constants.FieldKbId: req.KBIDs}})
	}
	if len(req.BizTags) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{constants.FieldBizTag: req.BizTags}})
	}
	return out
}

// buildFilter 把 VectorFilter 翻成 ES 查询。返回 false 表示条件全空。
func buildFilter(f repository.VectorFilter) (map[string]any, bool) {
	must := make([]any, 0, 3)

	if f.Account != "" {
		must = append(must, map[string]any{"term": map[string]any{constants.FieldAccount: f.Account}})
	}
	if f.KBID != 0 {
		must = append(must, map[string]any{"term": map[string]any{constants.FieldKbId: f.KBID}})
	}
	if f.DocID != 0 {
		must = append(must, map[string]any{"term": map[string]any{constants.FieldDocId: f.DocID}})
	}

	if len(must) == 0 {
		return nil, false
	}
	return map[string]any{"bool": map[string]any{"must": must}}, true
}

func sourceFields() []string {
	return []string{
		constants.FieldChunkId, constants.FieldDocId, constants.FieldKbId, constants.FieldOrder,
		constants.FieldTitle, constants.FieldContent, constants.FieldHeadingPath,
	}
}

func vectorDocBody(d repository.VectorDoc) map[string]any {
	return map[string]any{
		constants.FieldAccount:     d.Account,
		constants.FieldKbId:        d.KBID,
		constants.FieldDocId:       d.DocID,
		constants.FieldChunkId:     d.ChunkID,
		constants.FieldOrder:       d.Order,
		constants.FieldBizTag:      d.BizTag,
		constants.FieldTitle:       d.Title,
		constants.FieldContent:     d.Content,
		constants.FieldHeadingPath: d.HeadingPath,
		constants.FieldTitleVec:    d.TitleVec,
		constants.FieldContentVec:  d.ContentVec,
		constants.FieldCreateTs:    d.CreateTs,
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
	return truncateForLog(string(b))
}

// truncateForLog 按 rune 截断一段已经读进内存的文本。
//
// 分离出来是因为有的地方必须先把响应体整份读进来（要拿去 json.Unmarshal，
// 而 readBody 截断到 512 rune 会把它截坏），但报错时仍得截断。
func truncateForLog(s string) string {
	const maxLen = 512

	// 按 rune 截断：按字节截会把一个中文切成两半，日志里就是乱码
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "...(截断)"
}
