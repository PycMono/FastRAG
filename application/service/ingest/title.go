// 本文件是文档展示标题（knowledge_doc.title）的派生规则。
// 落库路径见 ingest.go，解析路径见 parse.go。

package ingest

import (
	"strings"
	"unicode"
)

// maxTitleLen title 的字符上限，与 knowledge_doc.title VARCHAR(255) 对齐。
const maxTitleLen = 255

// titleOfDoc 非解析路径的标题：就用 doc_name。
//
// markdown/text/chunks 三条路径都走这里——调用方自己挑了 doc_name，
// 请求体里的正文是不是带 H1 与我们无关（与解析路径的区别见 deriveTitle）。
func titleOfDoc(docName string) string {
	return truncateRunes(docName, maxTitleLen)
}

// deriveTitle 解析路径的标题：取 markdown 里的第一个 H1，取不到退回 doc_name。
//
// 为什么解析路径要特殊对待：这里的 doc_name 常常是 URL 末段（`1706.03762v7`），
// 拿它当展示标题毫无信息量，而 H1 才是文档自己的名字。
func deriveTitle(markdown, docName string) string {
	if h := h1Of(markdown); h != "" {
		return truncateRunes(h, maxTitleLen)
	}
	return truncateRunes(docName, maxTitleLen)
}

// h1Of 扫出第一个一级标题的正文；没有则返回空串。
//
// 只认「恰好一个 `#` 后面跟着空白」——`## x` 的第二个字符不是空白，天然不匹配。
// 逐行扫而不是只看首行：「第一个 H1」是文档语义，不是行号语义。
func h1Of(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		rest := line[1:]
		if rest == "" || !unicode.IsSpace(rune(rest[0])) {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}
