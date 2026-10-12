package ingest

import (
	"strings"
	"testing"
)

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		docName  string
		want     string
	}{
		{"有 H1", "# 呼吸衰竭的病因\n\n正文", "1706.03762v7", "呼吸衰竭的病因"},
		{"无 H1 退回 doc_name", "正文，没有标题\n## 二级", "a.pdf", "a.pdf"},
		{"只取第一个 H1", "## 二级\n\n# 第一个一级\n\n# 第二个一级", "a.pdf", "第一个一级"},
		{"H1 前面有空行与缩进", "\n\n   #   带空格的标题   \n正文", "a.pdf", "带空格的标题"},
		{"裸井号不算标题", "#\n正文", "a.pdf", "a.pdf"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveTitle(c.markdown, c.docName); got != c.want {
				t.Errorf("deriveTitle() = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestDeriveTitle_TruncatesTo255(t *testing.T) {
	long := "# " + strings.Repeat("标", 300)
	got := deriveTitle(long, "a.pdf")
	if n := len([]rune(got)); n != maxTitleLen {
		t.Errorf("截断后 %d 个字符，期望 %d", n, maxTitleLen)
	}
}

func TestTitleOfDoc_TruncatesTo255(t *testing.T) {
	got := titleOfDoc(strings.Repeat("名", 300))
	if n := len([]rune(got)); n != maxTitleLen {
		t.Errorf("截断后 %d 个字符，期望 %d", n, maxTitleLen)
	}
}
