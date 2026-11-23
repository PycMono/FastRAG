// chunksplit 用 FastRAG 自己的切片器把一份 markdown 切成「一块一个文件」，
// 便于喂给外部检索系统（每块 = 一个 md 文件）。
//
// 用法: chunksplit -in book.md -out dir -size 380
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/service"
)

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '，' || r == '、':
			b.WriteRune(' ')
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Join(strings.Fields(b.String()), "-")
	out = strings.Trim(out, "-")
	rs := []rune(out)
	if len(rs) > 30 {
		out = string(rs[:30])
	}
	if out == "" {
		out = "chunk"
	}
	return out
}

func main() {
	in := flag.String("in", "", "输入 markdown 文件")
	out := flag.String("out", "", "输出目录（一块一个 .md）")
	size := flag.Int("size", 380, "每块最大字符数")
	skipJunk := flag.Bool("skip-junk", false, "跳过封面/目录/表格等噪声块")
	minChunk := flag.Int("min", 0, "最小块字符数（0=不限制）")
	flag.Parse()

	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "用法: chunksplit -in book.md -out dir [-size 380]")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取失败:", err)
		os.Exit(1)
	}

	sp := service.NewSplitter()
	chunks, err := sp.Split(string(raw), constants.FormatMarkdown, service.ChunkOptions{
		ChunkSize: *size,
		MinChunk:  *minChunk,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "切片失败:", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "建目录失败:", err)
		os.Exit(1)
	}

	written := 0
	skipped := 0
	for i, c := range chunks {
		content := strings.TrimSpace(c.Content)
		if content == "" {
			continue
		}
		if *skipJunk && isJunk(c) {
			skipped++
			continue
		}
		// 首行（标题链）包成 markdown 标题，方便阅读与检索
		lines := strings.SplitN(content, "\n", 2)
		title := strings.TrimSpace(lines[0])
		body := ""
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}
		text := "# " + title + "\n\n" + body + "\n"
		name := fmt.Sprintf("%04d-%s.md", i+1, sanitize(c.Title))
		if err := os.WriteFile(filepath.Join(*out, name), []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写文件失败:", err)
			os.Exit(1)
		}
		written++
	}

	min, max, sum := 1<<30, 0, 0
	for _, c := range chunks {
		n := len([]rune(c.Content))
		if n < min {
			min = n
		}
		if n > max {
			max = n
		}
		sum += n
	}
	fmt.Printf("切出 %d 块，写出 %d 个文件，跳过噪声 %d 块\n", len(chunks), written, skipped)
	fmt.Printf("块大小: 最小 %d / 平均 %d / 最大 %d\n", min, sum/maxInt(1, len(chunks)), max)
}

// isJunk 粗筛噪声块：封面残片、目录行、表格、图片标记。
func isJunk(c *entity.Chunk) bool {
	body := strings.TrimSpace(c.Content)
	rs := []rune(body)
	if len(rs) < 40 {
		return true
	}
	if strings.Count(body, "|") > 8 && strings.Count(body, "|")*12 > len(rs) {
		return true
	}
	if strings.Count(body, "<!-- image -->") >= 2 && len(rs) < 260 {
		return true
	}
	title := strings.TrimSpace(c.Title)
	if title != "" {
		last := []rune(title)[len([]rune(title))-1]
		if unicode.IsDigit(last) { // 目录条目带页码
			return true
		}
	}
	digits := 0
	for _, r := range rs {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return float64(digits)/float64(len(rs)) > 0.25
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
