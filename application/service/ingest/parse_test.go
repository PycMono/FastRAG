package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/service"
)

// stubParserRegistry 固定返回一个实现；`boom: true` 时 Get 直接 panic。
//
// panic 用来钉住「某些检查必须挡在取后端之前」——顺序反了就会撞上它，用例判死。
// （本仓约定：桩里内嵌/故意炸，被不该调的路径调到时必须炸出声。）
type stubParserRegistry struct {
	parser interfaces.IParser
	boom   bool
}

func (r stubParserRegistry) Get() interfaces.IParser {
	if r.boom {
		panic("不该走到「取后端」这一步")
	}
	return r.parser
}

// stubParser 可控的解析实现。
type stubParser struct {
	name    string
	formats []string
	md      string
	err     error
}

func (p stubParser) Name() string      { return p.name }
func (p stubParser) Formats() []string { return p.formats }
func (p stubParser) Parse(context.Context, interfaces.ParseReq) (interfaces.ParseResult, error) {
	if p.err != nil {
		return interfaces.ParseResult{}, p.err
	}
	return interfaces.ParseResult{Markdown: p.md}, nil
}

// newParseService 造一个只够跑 Parse 的服务：KB 定位、向量化、落库都有桩子，
// 只有切片器是真实实现（Parse 的切片行为本身就值得覆盖）。
func newParseService(
	parsers interfaces.IParserRegistry, docRepo *stubDocRepo, store *stubStore,
	kbRepo *stubKBRepoDelta, tm *stubTm,
) *Service {
	return NewService(
		kbRepo, docRepo, store,
		okEmbeddingRegistry{e: stubEmbedding{dim: 4}},
		parsers,
		service.NewSplitter(),
		&stubIDService{},
		tm,
	)
}

// ─── 顺序：纯参数错误必须挡在解析之前 ────────────────────────────────────────
//
// 解析是本次请求里最贵、最慢的一步。格式不支持、scheme 不对、切片参数不对，
// 都不该白花一次解析的钱。
// （模型名是例外：合并落库段之后它由 ingestDoc 内部按名字取，晚于解析——见 parse.go 的注释。）
//
// 两道防线各管一段，缺一不可：
//   - 下游协作者（splitter / idGen / tm / docRepo / store）留成 nil，registry 留成
//     一被调到就 panic 的桩——真走到那一步就炸，把用例判死，钉住「根本没进到下游」；
//   - 对错误**文案**的断言——钉住到底是哪个检查先报了。
//
// 只看 err != nil 是不够的：顺序一反过来，浮上来的可能是别的错误，err 照样非空，
// 用例却照样绿。

// 格式不被该后端支持 → 参数错误，且在调用解析之前就返回。
func TestParse_UnsupportedFormat(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, nil,
		errEmbeddingRegistry{err: errors.New("不该走到这里")},
		stubParserRegistry{parser: stubParser{name: "docling", formats: []string{"pdf"}}},
		nil, nil, nil,
	)

	_, err := svc.Parse(context.Background(), &dto.DocParseDTO{
		Account: "demo", KBNo: "demo-kb", URL: "https://example.com/a.docx",
	})
	if err == nil {
		t.Fatal("docx 不在 pdf-only 后端的 Formats() 里，必须报参数错误")
	}
	// 文案必须点出是「格式不支持」报的，并带上后端名与扩展名：顺序反了、先落到
	// embedding 的哨兵上时，err 非空但不会提到这些。
	if !strings.Contains(err.Error(), "docling") || !strings.Contains(err.Error(), "docx") {
		t.Fatalf("应是格式检查报的错（提到 docling / docx），得到 %v", err)
	}
}

// 坏 scheme 必须挡在解析之前，而且先于「取后端」——registry 这里一被调到就 panic，
// 顺序反了用例就炸。
func TestParse_NonHTTPScheme_FailsEarly(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, nil,
		errEmbeddingRegistry{err: errors.New("不该走到这里")},
		stubParserRegistry{boom: true},
		nil, nil, nil,
	)

	for _, bad := range []string{"ftp://example.com/a.pdf", "file:///etc/passwd"} {
		_, err := svc.Parse(context.Background(), &dto.DocParseDTO{
			Account: "demo", KBNo: "demo-kb", URL: bad,
		})
		if err == nil {
			t.Fatalf("%s 必须被拒", bad)
		}
		// 文案必须来自 scheme 检查：别的错误同样非空，但只有真正的 scheme 检查
		// 才会提到 http/https——顺序反过来时浮上来的就不是它。
		if !strings.Contains(err.Error(), "http/https") {
			t.Fatalf("应是 scheme 检查报的错（提到 http/https），得到 %v", err)
		}
	}
}

// Review Focus 第 5 条：Formats() 为空表示「后端不声明、一律放行」。
func TestParse_EmptyFormats_AllowsAny(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	docRepo := &stubDocRepo{}
	store := &stubStore{}
	svc := newParseService(
		stubParserRegistry{parser: stubParser{name: "p", formats: nil, md: "# 标题\n正文正文正文"}},
		docRepo, store,
		&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
		&stubTm{},
	)

	out, err := svc.Parse(context.Background(), &dto.DocParseDTO{
		Account: "demo", KBNo: "kb-1", DocName: "a.pdf", URL: "https://example.com/a.pdf",
	})
	if err != nil {
		t.Fatalf("Formats() 为空时应一律放行，得到 %v", err)
	}
	if out.ChunkCount == 0 {
		t.Error("解析出的 markdown 应该被切成至少一个切片")
	}
}

// Review Focus 第 2 条：解析结果为空/全空白不能报成功。
func TestParse_EmptyMarkdown_Fails(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	for _, md := range []string{"", "   \n\t  \n"} {
		store := &stubStore{}
		svc := newParseService(
			stubParserRegistry{parser: stubParser{name: "p", md: md}},
			&stubDocRepo{}, store,
			&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
			&stubTm{},
		)

		if _, err := svc.Parse(context.Background(), &dto.DocParseDTO{
			Account: "demo", KBNo: "kb-1", DocName: "a.pdf", URL: "https://example.com/a.pdf",
		}); err == nil {
			t.Fatalf("markdown=%q 必须报错，不能当成功返回", md)
		}
		// 光报错还不够：真正要防的是「空文档被写进 ES」。空 markdown 必须在切片那步
		// 就停住，一次写都不该发生。
		if len(store.calls) != 0 {
			t.Fatalf("markdown=%q 是空的，不该有任何 ES 写入，得到 %v", md, store.calls)
		}
	}
}

// Review Focus 第 1 条：经 /docs/parse 解析一份已存在的 doc_name 时，
// 必须沿用库里那一行的 id（与 Ingest 的重导入同一条语义）。
func TestParse_Reimport_KeepsDocID(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	docRepo := &stubDocRepo{existing: &entity.KnowledgeDoc{ID: 42, KBID: 7, Name: "a.pdf", ChunkCount: 1}}
	store := &stubStore{}
	svc := newParseService(
		stubParserRegistry{parser: stubParser{name: "p", formats: []string{"pdf"}, md: "# 标题\n正文正文正文"}},
		docRepo, store,
		&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
		&stubTm{},
	)

	out, err := svc.Parse(context.Background(), &dto.DocParseDTO{
		Account: "demo", KBNo: "kb-1", DocName: "a.pdf", URL: "https://example.com/a.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.DocID != 42 {
		t.Errorf("同名重导入必须沿用 42，得到 %d（换了 id 就删不掉旧切片）", out.DocID)
	}
	if !store.refreshHit {
		t.Error("重导入必刷索引：DeleteExcept 只作用在已刷新的段上")
	}
}

// doc_name 缺省时由 URL 推出，且沿用既有的同名重导入语义（同一 URL 同名）。
func TestParse_DerivesDocNameFromURL(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	docRepo := &stubDocRepo{}
	svc := newParseService(
		stubParserRegistry{parser: stubParser{name: "p", md: "# 标题\n正文正文正文"}},
		docRepo, &stubStore{},
		&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
		&stubTm{},
	)

	out, err := svc.Parse(context.Background(), &dto.DocParseDTO{
		Account: "demo", KBNo: "kb-1", URL: "https://example.com/%E6%8A%A5%E5%91%8A.pdf?v=2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.DocName != "报告.pdf" {
		t.Errorf("doc_name 应取 URL 末段并做百分号解码，得到 %q", out.DocName)
	}
}

// ─── URL 工具 ────────────────────────────────────────────────────────────────

func TestIsHTTPScheme(t *testing.T) {
	for _, u := range []string{"http://a.com/x.pdf", "https://a.com/x.pdf", "HTTPS://a.com/x"} {
		if !isHTTPScheme(u) {
			t.Errorf("%q 应放行", u)
		}
	}
	for _, u := range []string{"ftp://a.com/x", "file:///etc/passwd", "", "://a.com", "a.com/x.pdf"} {
		if isHTTPScheme(u) {
			t.Errorf("%q 应拒绝", u)
		}
	}
}

// Review Focus 第 3 条：查询串与百分号编码。
func TestFilenameOf_QueryAndPercentEncoded(t *testing.T) {
	cases := []struct {
		url      string
		filename string
		ext      string
	}{
		{"https://example.com/a.pdf", "a.pdf", "pdf"},
		{"https://example.com/a.pdf?v=2#p3", "a.pdf", "pdf"},
		{"https://example.com/%E6%8A%A5%E5%91%8A.PDF?v=2", "报告.PDF", "pdf"},
		// 名字里本来就带 %25 的：只该解码一次，两次会把 %25 也吃掉
		{"https://example.com/100%2525.pdf", "100%25.pdf", "pdf"},
		{"https://example.com/dir/sub/b.docx", "b.docx", "docx"},
		{"https://example.com/", "", ""},
		{"https://example.com", "", ""},
		{"https://example.com/noext", "noext", ""},
	}

	for _, tc := range cases {
		if got := filenameOf(tc.url); got != tc.filename {
			t.Errorf("filenameOf(%q) = %q，期望 %q", tc.url, got, tc.filename)
		}
		if got := extOf(tc.url); got != tc.ext {
			t.Errorf("extOf(%q) = %q，期望 %q", tc.url, got, tc.ext)
		}
	}
}

// URL 末段推导出的名字要截到 512：url 允许 2048 字符，
// 推导结果可以合法地超过 knowledge_doc.name 的列宽。
func TestDeriveDocName_Truncated(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("啊", 600) + ".pdf"
	if n := len([]rune(deriveDocName(long))); n != maxDocNameLen {
		t.Errorf("应截到 %d 个字符，得到 %d", maxDocNameLen, n)
	}
}

// 哈希兜底名稳定：同一 URL 恒得同名，否则同名重导入会被当成新文档。
func TestDeriveDocName_HashFallbackStable(t *testing.T) {
	u := "https://example.com/" // 末段是 "/"，推不出可读名
	a, b := deriveDocName(u), deriveDocName(u)
	if a != b {
		t.Errorf("同一 URL 必须得到同一个名字：%q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "parsed-") {
		t.Errorf("推不出文件名时应退化成 parsed-<hash>，得到 %q", a)
	}
}
