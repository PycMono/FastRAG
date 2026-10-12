// 本文件是基于 URL 的文档解析导入：解析成 markdown，再走与 Ingest 相同的下游。
// 服务主体见 service.go，单文档导入见 ingest.go，批量导入见 batch.go。

package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	neturl "net/url"
	"path"
	"strings"
	"unicode"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
)

// Parse 解析 URL 上的文档并入库。等同 Ingest，但 chunks 来自解析结果而不是请求体。
//
// 顺序是刻意的：能提前判的纯参数错误（scheme、格式、切片参数）都在调用解析后端之前
// 报完——解析是本次请求里最贵、最慢的一步，不该为一条坏参数白花一次解析的钱。
//
// 例外是模型名：向量的实现由 ingestDoc 内部按 model 取（它收名字、不收实现），
// 所以拼错的模型名要等解析跑完才报——这是合并落库段换来的（用户 2026-10-12 裁定）。
func (s *Service) Parse(ctx context.Context, in *dto.DocParseDTO) (*vo.DocIngestVO, error) {
	kb, err := s.kbRepo.FindByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	// URL 的 scheme 由我们判：gin 的 `url` tag 只要求「带 host 的绝对 URL」，
	// `ftp://…` 也放行，而两个后端都只认 http(s)。放在解析之前——坏 scheme
	// 不该白花一次解析的钱（§5、§9）。
	if !isHTTPScheme(in.URL) {
		return nil, apperrors.NewParamError("url 只支持 http/https: " + in.URL)
	}

	// 用哪家解析后端完全由配置决定（config.parser.default）：请求不带这个参数，
	// 想换后端就改配置重启。
	parser := s.parsers.Get()
	if !supportsFormat(parser, in.URL) {
		return nil, apperrors.NewParamError(
			"解析后端 " + parser.Name() + " 不支持该文件类型: " + filenameOf(in.URL))
	}

	opts, err := in.ChunkOptions()
	if err != nil {
		return nil, err
	}

	docName := in.DocName
	if docName == "" {
		docName = deriveDocName(in.URL)
	}

	res, err := parser.Parse(ctx, interfaces.ParseReq{
		URL:      in.URL,
		Filename: filenameOf(in.URL),
	})
	if err != nil {
		return nil, err
	}

	// 解析产物恒是 markdown，走结构感知切片器（与 format=markdown 同一条路）。
	return s.ingestDoc(ctx, kb, docName, func() (entity.Chunks, error) {
		return s.splitter.Split(res.Markdown, constants.FormatMarkdown, opts)
	}, in.Model, in.Refresh)
}

// supportsFormat 判 URL 的扩展名是否在该后端声明的格式内。
//
// 两条放行：Formats() 为空（后端不声明），或扩展名推不出来（URL 没有文件名）。
// 后者交给后端自己判——总比在这里误杀一个合法地址强。
func supportsFormat(p interfaces.IParser, rawURL string) bool {
	formats := p.Formats()
	if len(formats) == 0 {
		return true
	}
	ext := extOf(rawURL)
	if ext == "" {
		return true
	}
	for _, f := range formats {
		if strings.EqualFold(f, ext) {
			return true
		}
	}
	return false
}

// filenameOf 取 URL 末段作为文件名（去查询串、百分号解码后）；推不出返回空串。
//
// 不再自己 PathUnescape：net/url 存进 u.Path 的**已经是解码后的**路径
// （`/%47%6f` 解析出来就是 `/Go`），再解一次是第二次解码——名字里本来就带
// `%25` 的文件会被解坏（`100%2525.pdf` 变成 `100%.pdf`）。
func filenameOf(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// maxDocNameLen doc_name 的字符上限，与 knowledge_doc.name VARCHAR(512) 对齐。
const maxDocNameLen = 512

// isHTTPScheme 只放行 http/https。Docling 的 kind:"http" 与腾讯都只认这两个。
func isHTTPScheme(rawURL string) bool {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// deriveDocName doc_name 缺省值：优先用 URL 末段，推不出就退化成一个稳定的哈希名。
//
// 用 URL 的哈希而不是时间戳：同一篇文档重复解析应当落到同一行（同名重导入视作更新），
// 哈希保证这一点。
//
// 返回值截到 maxDocNameLen：`binding:"max=512"` 只管调用方传进来的 doc_name，
// 而 url 允许到 2048 字符，末段推导出的名字可以合法地超过列的宽度——不在入口截断
// 就会一路带到 ⑨ 那步才被 MySQL 拒掉，那时 ES 已经写完了。截断可能切掉扩展名，
// 无所谓：doc_name 只是个标签。
func deriveDocName(rawURL string) string {
	name := filenameOf(rawURL)
	if name == "" {
		sum := sha256.Sum256([]byte(rawURL))
		name = "parsed-" + hex.EncodeToString(sum[:])[:12]
	}
	return truncateRunes(name, maxDocNameLen)
}

// truncateRunes 按 rune 截断。VARCHAR(512) 数的是**字符**，按字节截既可能劈坏
// 多字节字符、也不是列宽的真实含义。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// extOf 取扩展名（小写、不含点）；推不出则空串。
//
// 只有「最后一段里最后一个点之后全是字母」才认作扩展名。光用 path.Ext 不够：
// arXiv 那种省略扩展名的 id（`/pdf/1706.03762v7`）里带点，path.Ext 会把
// `03762v7` 当成扩展名，于是格式闸门把一条合法地址误杀——正是 supportsFormat
// 注释里说要避免的那件事。
func extOf(rawURL string) string {
	name := filenameOf(rawURL)
	if name == "" {
		return ""
	}
	ext := path.Ext(name)
	if !isLetterExt(ext) {
		return ""
	}
	return strings.ToLower(ext[1:])
}

// isLetterExt 判 path.Ext 的结果「像个真扩展名」：点后面全是字母（.pdf、.docx）。
// 点后面带数字或符号的（.03762v7）不是扩展名，是文件名的一部分。
func isLetterExt(ext string) bool {
	if len(ext) < 2 {
		return false
	}
	for _, r := range ext[1:] {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
