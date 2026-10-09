package migration

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	apperrors "github.com/PycMono/FastRAG/common/errors"
)

//go:embed schema.sql
var schemaFS embed.FS

// schemaSQL 是建表语句的单一事实源，随二进制一起发布。
// 这样「本地 docker 初始化」和「生产部署」用的是同一份 DDL，不会漂移。
var schemaSQL = mustReadSchema()

func mustReadSchema() string {
	b, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		// embed 的文件缺失属于构建期错误，运行期不可能发生
		panic(fmt.Sprintf("migration: 读取内嵌 schema.sql 失败: %v", err))
	}
	return string(b)
}

// Schema 返回内嵌的建表语句原文，便于外部做 diff、打印或交接给 DBA。
func Schema() string { return schemaSQL }

// Run 在 db 上执行内嵌的 schema。
//
// 幂等：DDL 全部是 CREATE ... IF NOT EXISTS，重复执行安全。
//
// 参数用标准库的 *sql.DB 而不是 *gorm.DB，是为了让本包零业务依赖——
// GORM 用户在装配层做一次转换即可：
//
//	sqlDB, err := gormDB.DB()
//	migration.Run(ctx, sqlDB)
func Run(ctx context.Context, db *sql.DB) error {
	for i, stmt := range Split(schemaSQL) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return apperrors.NewSysError(apperrors.CodeInternal,
				fmt.Sprintf("migration: 执行第 %d 条语句失败: %.120s", i+1, stmt)).Wrap(err)
		}
	}
	return nil
}

// Split 把一段 SQL 脚本切成可逐条执行的语句。
//
// 为什么不能直接 strings.Split(sql, ";")：
//   - 行注释 -- 和块注释 /* */ 里可能合法地出现分号
//   - 字符串字面量里的分号更是正常内容（比如存了一段 JSON）
//
// 所以这里走一个最小的状态机：剥掉注释、跳过引号内的内容，只在「代码位置」
// 的分号上断开。MySQL 驱动默认不开 multiStatements，逐条执行是唯一稳妥的做法。
//
// 注意全程按 rune 遍历：中文字符是 3 字节，一旦混用字节偏移去推进
// rune 下标，遇到中文注释就会算飞（这不是理论风险，是踩过的坑）。
func Split(script string) []string {
	var (
		stmts []string
		buf   strings.Builder

		inQuote  rune // 当前所在的引号字符，0 表示不在引号内
		inLineCm bool // 是否在 -- 行注释里
	)

	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		// 行注释：吃到行尾，换行本身保留，让后续报错定位还能看出原结构
		if inLineCm {
			if c == '\n' {
				inLineCm = false
				buf.WriteRune(c)
			}
			continue
		}

		if inQuote != 0 {
			buf.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				// 转义：把下一个字符原样吞掉，避免 \' 被误判成字符串结束
				i++
				buf.WriteRune(runes[i])
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
			continue
		}

		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			inLineCm = true
			i++

		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			// 块注释：找到 */，用一个空格顶替（不能直接删，
			// 否则 `SELECT/*c*/1` 会被粘成 `SELECT1`）
			j := i + 2
			for j+1 < len(runes) && !(runes[j] == '*' && runes[j+1] == '/') {
				j++
			}
			i = j + 1 // 落在 '/' 上，外层 i++ 后从注释之后继续
			buf.WriteRune(' ')

		case c == '\'' || c == '"' || c == '`':
			inQuote = c
			buf.WriteRune(c)

		case c == ';':
			if s := strings.TrimSpace(buf.String()); s != "" {
				stmts = append(stmts, s)
			}
			buf.Reset()

		default:
			buf.WriteRune(c)
		}
	}

	if s := strings.TrimSpace(buf.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}
