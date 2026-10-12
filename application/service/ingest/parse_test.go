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

// stubParserRegistry 固定返回一个实现，或固定报错。
type stubParserRegistry struct {
	parser interfaces.IParser
	err    error
}

func (r stubParserRegistry) Get(name string) (interfaces.IParser, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.parser, nil
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
// 解析是本次请求里最贵、最慢的一步。名字写错、格式不支持、scheme 不对，
// 都不该白花一次解析的钱——这些用例里 embeddings / splitter 故意留成会报错或 nil 的，
// 走到那儿就说明顺序反了。

// 未知解析后端必须在写任何东西之前返回——不能先切片、先算向量、先写 ES。
func TestParse_UnknownParser_FailsEarly(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, // docRepo
		nil, // store
		errEmbeddingRegistry{err: errors.New("不该走到这里")},
		stubParserRegistry{err: errors.New(`没有名为 "nope" 的解析后端。可用：[docling]`)},
		nil, // splitter
		nil, // idGen
		nil, // tm
	)

	_, err := svc.Parse(context.Background(), &dto.DocParseDTO{
		Account: "demo", KBNo: "demo-kb", URL: "https://example.com/a.pdf", Parser: "nope",
	})
	if err == nil {
		t.Fatal("未知 parser 必须报错")
	}
}

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
}

// 坏 scheme 必须挡在解析之前，而且先于「取后端」——registry 这里故意报错，
// 若先调它就说明顺序反了。
func TestParse_NonHTTPScheme_FailsEarly(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, nil,
		errEmbeddingRegistry{err: errors.New("不该走到这里")},
		stubParserRegistry{err: errors.New("不该走到这里")},
		nil, nil, nil,
	)

	for _, bad := range []string{"ftp://example.com/a.pdf", "file:///etc/passwd"} {
		if _, err := svc.Parse(context.Background(), &dto.DocParseDTO{
			Account: "demo", KBNo: "demo-kb", URL: bad,
		}); err == nil {
			t.Fatalf("%s 必须被拒", bad)
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
		svc := newParseService(
			stubParserRegistry{parser: stubParser{name: "p", md: md}},
			&stubDocRepo{}, &stubStore{},
			&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
			&stubTm{},
		)

		if _, err := svc.Parse(context.Background(), &dto.DocParseDTO{
			Account: "demo", KBNo: "kb-1", DocName: "a.pdf", URL: "https://example.com/a.pdf",
		}); err == nil {
			t.Fatalf("markdown=%q 必须报错，不能当成功返回", md)
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
