package migration

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "空脚本",
			script: "",
			want:   nil,
		},
		{
			name:   "只有注释",
			script: "-- 什么都没有\n-- 真的没有\n",
			want:   nil,
		},
		{
			name:   "两条语句",
			script: "SELECT 1; SELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "末尾无分号也要收进来",
			script: "SELECT 1; SELECT 2",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "行注释里的分号不该断开",
			script: "SELECT 1 -- 这里有个;分号\n;",
			want:   []string{"SELECT 1"},
		},
		{
			// 中文注释是 3 字节/字，字节偏移和 rune 偏移混用会算飞。
			// 这一条就是专门盯这个的。
			name:   "中文块注释里的分号不该断开",
			script: "SELECT /* 注释里的;分号 */ 1;",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "块注释两侧无空格也不能粘成一个词",
			script: "SELECT/*c*/1;",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "字符串里的分号不该断开",
			script: `INSERT INTO t VALUES ('a;b');`,
			want:   []string{`INSERT INTO t VALUES ('a;b')`},
		},
		{
			name:   "转义引号不该提前结束字符串",
			script: `INSERT INTO t VALUES ('it\'s; ok');`,
			want:   []string{`INSERT INTO t VALUES ('it\'s; ok')`},
		},
		{
			name:   "反引号标识符里的分号",
			script: "INSERT INTO `we;ird` VALUES (1);",
			want:   []string{"INSERT INTO `we;ird` VALUES (1)"},
		},
		{
			name:   "连续分号不产生空语句",
			script: "SELECT 1;;;SELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "未闭合的块注释吃到结尾",
			script: "SELECT 1; /* 没关\nSELECT 2;",
			want:   []string{"SELECT 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.script)

			// 注释被顶替成一个空格，所以语句内部的空白数量不固定。
			// 对 SQL 来说空白数量无意义，这里统一折叠后再比，
			// 免得测试被「两个空格还是三个空格」这种噪声绑住。
			norm := func(ss []string) []string {
				if ss == nil {
					return nil
				}
				out := make([]string, len(ss))
				for i, s := range ss {
					out[i] = strings.Join(strings.Fields(s), " ")
				}
				return out
			}

			if !reflect.DeepEqual(norm(got), norm(tt.want)) {
				t.Fatalf("Split() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestSplit_SchemaProducesExpectedStatements 保证内嵌的 schema 能被切成
// 可执行语句，且条数与人工统计一致。
//
// 价值在于：schema.sql 里一旦出现「注释里的分号把语句切断」或
// 「漏写分号导致两条语句粘成一条」，这里立刻发现，
// 而不是等到某个环境上手工跑 DDL 才炸。
func TestSplit_SchemaProducesExpectedStatements(t *testing.T) {
	stmts := Split(Schema())

	// CREATE DATABASE + USE + 2 张表 = 4 条
	if len(stmts) != 4 {
		t.Fatalf("切出 %d 条语句, want 4: %#v", len(stmts), stmts)
	}
	for i, s := range stmts {
		if strings.TrimSpace(s) == "" {
			t.Errorf("第 %d 条为空", i+1)
		}
	}
}

// TestSchema_ContainsAllTables 是一道防退化闸门：
// 有人编辑 schema.sql 时误删了一张表，这里会红。
func TestSchema_ContainsAllTables(t *testing.T) {
	schema := Schema()

	want := []string{
		"CREATE DATABASE IF NOT EXISTS `fastrag`",
		"USE `fastrag`",
		"CREATE TABLE IF NOT EXISTS `knowledge_base`",
		"CREATE TABLE IF NOT EXISTS `knowledge_doc`",
	}
	for _, w := range want {
		if !strings.Contains(schema, w) {
			t.Errorf("schema 中缺少: %s", w)
		}
	}

	// 反向断言：D3 明确「切片不入 MySQL」，这张表不能出现
	if strings.Contains(schema, "CREATE TABLE IF NOT EXISTS `knowledge_chunk`") {
		t.Error("切片下沉 ES 是核心决策 D3，MySQL 里不应出现切片表")
	}

	// 反向断言：D2 是单索引多租户，不靠 account→index 路由表。
	// 留一张 tenant_index 说明分桶方案没删干净。
	if strings.Contains(schema, "CREATE TABLE IF NOT EXISTS `tenant_index`") {
		t.Error("索引策略已改为单索引 + term(account)（D2），不应再有 tenant_index 路由表")
	}

	// 反向断言：本期不做对象存储权威副本，canonical_path 这类「回灌之源」的列不该在。
	if strings.Contains(schema, "canonical_path") {
		t.Error("本期不做对象存储权威副本（无回灌），MySQL 里不应再有 canonical_path")
	}

	// 反向断言：本期不做发布与版本管理（设计文档 §1.2），发布台账不该在。
	if strings.Contains(schema, "kb_publish_log") {
		t.Error("本期不做知识库发布与版本（§1.2），不应再有 kb_publish_log 发布台账")
	}
}

// TestSchema_DDLIsIdempotent 确认建表语句都带 IF NOT EXISTS，
// 否则重复执行 Run() 会直接报错。
func TestSchema_DDLIsIdempotent(t *testing.T) {
	for _, line := range strings.Split(Schema(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CREATE TABLE") {
			continue
		}
		if !strings.Contains(line, "IF NOT EXISTS") {
			t.Errorf("建表语句必须幂等，缺 IF NOT EXISTS: %s", line)
		}
	}
}

// TestSchema_CriticalColumns 守住几条「写错了不会报错、只会静默出错」的列。
func TestSchema_CriticalColumns(t *testing.T) {
	schema := Schema()

	checks := []struct {
		desc    string
		pattern string
	}{
		{"knowledge_doc.id 不得自增（雪花预分配）", "`id`              BIGINT UNSIGNED NOT NULL COMMENT '雪花 ID"},
		{"文档唯一键必须是 (kb_id, name)，不是 content_hash", "UNIQUE KEY `uk_kb_name` (`kb_id`, `name`, `delete_ts`)"},
	}
	for _, c := range checks {
		if !strings.Contains(schema, c.pattern) {
			t.Errorf("%s\n  期望包含: %s", c.desc, c.pattern)
		}
	}

	// knowledge_doc 的主键不能有 AUTO_INCREMENT
	docBlock := schema[strings.Index(schema, "CREATE TABLE IF NOT EXISTS `knowledge_doc`"):]
	docBlock = docBlock[:strings.Index(docBlock, "ENGINE=InnoDB")]
	if strings.Contains(docBlock, "AUTO_INCREMENT") {
		t.Error("knowledge_doc.id 是雪花预分配的，不能自增——" +
			"写入先落 ES、需要先知道 doc_id 才能组织切片，等自增 ID 会把顺序反过来")
	}
}
