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

// FindByIDs 的 SELECT 清单里同样绝不能出现 content 列本身。
//
// 这条钉的是内存而不是响应体：检索的存活校验只为回答「这批 doc_id 里哪几行还在」，
// 而单篇正文上限 8 MiB（constants.MaxContentBytes）——带上 content 就是把几十 MB
// 读进内存做一次存在性判断。要正文的是详情接口，它走 FindByID。
//
// 判据同 TestListByKBSelect_NoContentColumn：逗号切分后的整项等于 content，
// 不用 strings.Contains（content_hash 也含这个子串，它是合法的）。
func TestFindByIDsSelect_NoContentColumn(t *testing.T) {
	for _, col := range strings.Split(findByIDsSelect, ",") {
		if strings.TrimSpace(col) == "content" {
			t.Fatal("存活校验的 SELECT 里带了 content 列——每次检索都会把命中文档的全文读回内存")
		}
	}
	// account 与 kb_id 必须留着：前者是 docview 的租户校验依据，
	// 后者是详情要拿去回查 KB 行（source_url 是库级的）
	for _, need := range []string{"account", "kb_id"} {
		if !strings.Contains(findByIDsSelect, need) {
			t.Errorf("SELECT 里没有 %s：调用方要用它（租户校验 / 回查 KB 行）", need)
		}
	}
}
