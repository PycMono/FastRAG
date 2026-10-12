package persistence

import (
	"strings"
	"testing"
)

// ListByKB 的 SELECT 清单里绝不能出现 content 列本身。
//
// 这条决定是「列表接口的响应体有界」的全部依据，而它只写在一条 SQL 字符串里、
// 跑起来也不报错（多读一列只是慢）。本仓没有真库测试设施，所以这里直接钉字符串。
//
// 判据用「逗号切分后的整项等于 content」，不用 strings.Contains：
// content_hash 与 has_content 都含 content 子串，它们是合法的。
func TestListByKBSelect_NoContentColumn(t *testing.T) {
	for _, col := range strings.Split(listByKBSelect, ",") {
		if strings.TrimSpace(col) == "content" {
			t.Fatal("SELECT 里带了 content 列——列表接口的响应体就没有上界了")
		}
	}
	if !strings.Contains(listByKBSelect, "AS has_content") {
		t.Error("没有 has_content 表达式：列表就不知道哪篇有原文，页面只能逐篇去拉正文")
	}
	if !strings.Contains(listByKBSelect, "title") {
		t.Error("SELECT 里没有 title：列表拿什么显示文档名")
	}
}
