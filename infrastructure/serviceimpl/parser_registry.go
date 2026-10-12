// 本文件是解析后端注册表：启动时给 config.parser.default 造出**那一个**实现。
//
// 与 embedding / rerank 那两张表的差别：它们的名字来自请求，这张表的名字来自配置
// （接口不带选后端的参数，用户 2026-10-12 裁定），所以建出来的就是一家——
// 换后端＝改配置再重启。
//
// 启动时一次建好、之后只读，天然并发安全，不需要锁。

package serviceimpl

import (
	"fmt"

	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// parserRegistry 只装 default 指的那一家。
type parserRegistry struct {
	def interfaces.IParser
}

// NewParserRegistry 启动时给 default 造实现。
//
// 配置错在这一步就暴露，而不是等某次解析跑到一半才炸。
//
// 只造 default：其余 entry 由 config.validate 校验字段，但不构造——构造会碰密钥
// 这类「真用起来才需要」的东西，而 config.example.json 里 tencent 的密钥是空的，
// 全量构造会让「cp 示例再启动」直接失败（见 parser_tencent.go 的构造函数）。
func NewParserRegistry(conf *config.Config) (interfaces.IParserRegistry, error) {
	p, err := conf.Parser.Params("")
	if err != nil {
		return nil, err
	}
	switch p.Type {
	case config.ParserTypeDocling:
		return &parserRegistry{def: newDoclingParser(p)}, nil
	default:
		// validate 已经挡过一次，这里兜底，避免将来加 type 时漏改
		return nil, fmt.Errorf("解析后端 %q 的 type=%q 没有对应实现", p.Name, p.Type)
	}
}

// Get 给出配置选定的那个解析实现。
func (r *parserRegistry) Get() interfaces.IParser { return r.def }
