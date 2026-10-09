package service

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
)

// Splitter 切片领域服务。
//
// 放 domain 而不是 application：「怎么切才算对」是业务规则，不是流程编排。
// 它不碰数据库、不发网络请求，所以可以纯单测。
type Splitter struct{}

func NewSplitter() *Splitter { return &Splitter{} }

// Split 按形态切分。format = chunks 不走这里——调用方已经切好了，走 Normalize。
func (s *Splitter) Split(content, format string, opts ChunkOptions) (entity.Chunks, error) {
	nOpts, err := opts.Normalize()
	if err != nil {
		return nil, err
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return nil, apperrors.NewParamError("content 不能为空")
	}

	var chunks entity.Chunks
	switch format {
	case constants.FormatMarkdown:
		chunks = splitMarkdown(content, nOpts)
	case constants.FormatText:
		chunks = splitBySeparators(content, "", "", nOpts.ChunkSize)
	default:
		return nil, apperrors.NewParamError("不支持的 format: " + format)
	}

	return finalize(chunks, nOpts), nil
}

// Normalize 归一化调用方直接给出的切片（format = chunks）。
// 不重切，只做三件事：滤空、补标题链、重新编号。
func (s *Splitter) Normalize(in []ChunkInput) (entity.Chunks, error) {
	out := make(entity.Chunks, 0, len(in))
	for _, c := range in {
		body := strings.TrimSpace(c.Content)
		if body == "" {
			continue
		}
		title := strings.TrimSpace(c.Title)
		out = append(out, &entity.Chunk{
			Title:       title,
			HeadingPath: title,
			Content:     renderPiece(title, body),
		})
	}
	if len(out) == 0 {
		return nil, apperrors.ErrDocChunkEmpty
	}
	for i, c := range out {
		c.Order = i
	}
	return out, nil
}

// ─── markdown 结构感知（§8.1）─────────────────────────────────────────────────

var (
	// ATX 标题：# ~ ######，允许结尾的闭合 #
	reATXHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	// 代码围栏开关
	reFence = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
)

// mdBlock 一个标题小节：只含「本级的正文」，不含子标题的正文。
type mdBlock struct {
	level int
	title string
	path  string // 标题链，"一级，二级"
	body  string
}

// parseMarkdownBlocks 按标题把 markdown 切成有序小节，并维护标题链。
//
// 代码围栏内的 # 不算标题——这是最容易出错的地方：
// 文档里嵌一段 shell 注释就能把整篇的层级结构带偏，而且不会报错。
func parseMarkdownBlocks(content string) []mdBlock {
	var (
		blocks []mdBlock
		stack  []string // 标题栈：stack[i] 是当前的第 i+1 级标题
		cur    mdBlock
		fence  string // 非空表示正在代码围栏内
	)

	flush := func() {
		if strings.TrimSpace(cur.body) != "" {
			blocks = append(blocks, cur)
		}
		cur = mdBlock{}
	}

	for _, line := range strings.Split(content, "\n") {
		if m := reFence.FindStringSubmatch(line); m != nil {
			if fence == "" {
				fence = m[1]
			} else if strings.HasPrefix(m[1], fence[:1]) {
				fence = ""
			}
			cur.body += line + "\n"
			continue
		}
		if fence != "" {
			cur.body += line + "\n"
			continue
		}

		m := reATXHeading.FindStringSubmatch(line)
		if m == nil {
			cur.body += line + "\n"
			continue
		}

		flush()

		level := len(m[1])
		title := strings.TrimSpace(m[2])

		if level-1 < len(stack) {
			stack = stack[:level-1]
		}
		// 跳级（h2 直接跟 h4）时补空位，保证「栈深度 == 标题级别」
		for len(stack) < level-1 {
			stack = append(stack, "")
		}
		stack = append(stack, title)

		cur = mdBlock{level: level, title: title, path: joinTitlePath(stack)}
	}
	flush()

	return blocks
}

func joinTitlePath(stack []string) string {
	parts := make([]string, 0, len(stack))
	for _, t := range stack {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "，")
}

// lastSegment 取标题链的最后一段。
func lastSegment(path string) string {
	const sep = "，"
	if i := strings.LastIndex(path, sep); i >= 0 {
		return path[i+len(sep):]
	}
	return path
}

// renderPiece 把标题链拼到正文前面。
//
// 这就是那个廉价版的 Contextual Retrieval：切片一旦离开文档，
// 单看正文常常不知道自己在讲谁；把标题链写进去，BM25 和 embedding 都能吃到。
func renderPiece(path, body string) string {
	if path == "" {
		return body
	}
	return path + "\n" + body
}

// splitMarkdown 结构感知切分：小节点合并，超长节点降级递归。
func splitMarkdown(content string, opts ChunkOptions) entity.Chunks {
	var (
		chunks  entity.Chunks
		buf     strings.Builder
		bufPath string
		bufLen  int
	)

	flush := func() {
		text := strings.TrimSpace(buf.String())
		if text != "" {
			chunks = append(chunks, &entity.Chunk{
				Title:       lastSegment(bufPath),
				HeadingPath: bufPath,
				Content:     text,
			})
		}
		buf.Reset()
		bufLen = 0
	}

	for _, b := range parseMarkdownBlocks(content) {
		body := strings.TrimSpace(b.body)
		if body == "" {
			continue
		}

		piece := renderPiece(b.path, body)
		n := runeLen(piece)

		if n > opts.ChunkSize {
			// 单个节点就超长：先落掉攒着的，再把这个节点降级递归
			flush()
			chunks = append(chunks,
				splitBySeparators(body, lastSegment(b.path), b.path, opts.ChunkSize)...)
			continue
		}

		// 只在同一标题链下合并。跨标题合并虽然能凑满长度，
		// 但切片的 HeadingPath 会变得名不副实——宁短一点也别骗人
		if bufLen > 0 && (bufPath != b.path || bufLen+n > opts.ChunkSize) {
			flush()
		}
		if bufLen == 0 {
			bufPath = b.path
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
			bufLen += 2
		}
		buf.WriteString(piece)
		bufLen += n
	}
	flush()

	return chunks
}

// ─── 兜底切片器（§8.2）────────────────────────────────────────────────────────

// separators 递归降级用的分隔符：从「语义边界明确」到「只能硬切」。
var separators = []string{
	"\n\n", "\n",
	"。", "！", "？", "；",
	". ", "! ", "? ", "; ",
	"，", ", ", " ",
}

// splitBySeparators 递归降级切分。
//
// 先拿最强的分隔符切；段还是超长就换更弱的分隔符再切，
// 直到切得动、或者分隔符用尽只能硬切。
// 比「按固定长度硬切」强的地方在于：它优先在语义边界断开。
func splitBySeparators(text, title, path string, size int) entity.Chunks {
	prefix := ""
	if path != "" {
		prefix = path + "\n"
	}

	// 前缀也占预算，否则长标题会把正文挤没
	budget := size - runeLen(prefix)
	if budget < size/4 {
		budget = size / 4
	}

	return splitRec(strings.TrimSpace(text), prefix, title, path, budget, 0)
}

func splitRec(text, prefix, title, path string, budget, depth int) entity.Chunks {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if runeLen(text) <= budget {
		return entity.Chunks{{
			Title: title, HeadingPath: path, Content: prefix + text,
		}}
	}

	// 分隔符用尽仍超长：只能硬切。宁可切碎，也不能丢内容
	if depth >= len(separators) {
		return hardCut(text, prefix, title, path, budget)
	}

	sep := separators[depth]

	var (
		out    entity.Chunks
		cur    strings.Builder
		curLen int
	)

	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, &entity.Chunk{
				Title: title, HeadingPath: path, Content: prefix + s,
			})
		}
		cur.Reset()
		curLen = 0
	}

	for _, part := range strings.Split(text, sep) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if runeLen(part) > budget {
			// 这一段自己就超预算：落掉手上的，对它再降一级
			flush()
			out = append(out, splitRec(part, prefix, title, path, budget, depth+1)...)
			continue
		}

		if curLen > 0 && curLen+runeLen(part)+len(sep) > budget {
			flush()
		}
		if curLen > 0 {
			cur.WriteString(sep)
			curLen += len(sep)
		}
		cur.WriteString(part)
		curLen += runeLen(part)
	}
	flush()

	return out
}

// hardCut 兜底中的兜底：按 rune 硬切。
// 走到这一步说明文本里连一个空格都没有（base64、无空格长串），
// 切碎了也比丢了强。
func hardCut(text, prefix, title, path string, budget int) entity.Chunks {
	if budget < 1 {
		budget = 1
	}

	runes := []rune(text)
	out := make(entity.Chunks, 0, len(runes)/budget+1)
	for len(runes) > 0 {
		n := budget
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, &entity.Chunk{
			Title: title, HeadingPath: path, Content: prefix + string(runes[:n]),
		})
		runes = runes[n:]
	}
	return out
}

// finalize 合并过短切片并重新编号。
//
// 过短切片（比如只有一行标题、结论只有三个字的节点）在向量检索里
// 几乎必然是噪声：它的向量不携带判别信息，却要占掉一个 topK 名额。
func finalize(chunks entity.Chunks, opts ChunkOptions) entity.Chunks {
	out := make(entity.Chunks, 0, len(chunks))

	for _, c := range chunks {
		if len(out) > 0 && runeLen(c.Content) < opts.MinChunk {
			prev := out[len(out)-1]
			if merged := prev.Content + "\n\n" + c.Content; runeLen(merged) <= opts.ChunkSize {
				prev.Content = merged
				continue
			}
		}
		out = append(out, c)
	}

	for i, c := range out {
		c.Order = i
	}
	return out
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }
