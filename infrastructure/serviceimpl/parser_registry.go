// 本文件是解析后端注册表：把配置里的 backends 造成一组 IParser，按名字取用。
//
// 与 embedding / rerank 的注册表同构：启动时一次建好、之后只读，天然并发安全，
// 不需要锁。

package serviceimpl

import (
	"fmt"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// parserRegistry 按名字取解析实现。
type parserRegistry struct {
	conf   config.ParserConfig
	byName map[string]interfaces.IParser
}

// NewParserRegistry 启动时按 type 给每个 entry 造一个实现。
//
// 配置错在这一步就暴露，而不是等某次解析跑到一半才炸。
func NewParserRegistry(conf *config.Config) (interfaces.IParserRegistry, error) {
	c := conf.Parser

	r := &parserRegistry{
		conf:   c,
		byName: make(map[string]interfaces.IParser, len(c.Backends)),
	}
	for _, name := range c.Names() {
		p, err := c.Params(name)
		if err != nil {
			return nil, err
		}
		switch p.Type {
		case config.ParserTypeDocling:
			r.byName[name] = newDoclingParser(p)
		default:
			// validate 已经挡过一次，这里兜底，避免将来加 type 时漏改
			return nil, fmt.Errorf("解析后端 %q 的 type=%q 没有对应实现", name, p.Type)
		}
	}
	return r, nil
}

// Get 取解析实现，名字为空时回落到配置里的 default。
//
// 解析（空→default、未知→报错时列出可用名字）交给 config.Params，这里只查表：
// 那份解析在启动构造与请求解析两处必须给出同一个答案。
func (r *parserRegistry) Get(name string) (interfaces.IParser, error) {
	p, err := r.conf.Params(name)
	if err != nil {
		return nil, apperrors.NewParamError(err.Error())
	}
	return r.byName[p.Name], nil
}
