// 本文件是模型注册表：把配置里的一组服务商造成一组实现，按名字取用。
//
// 两个注册表都在**启动时一次性建好**，之后只读——map 建好之后再没写过，
// 天然并发安全，不需要锁（批量导入的 4 个 goroutine 各自 Get 互不干扰）。

package serviceimpl

import (
	"fmt"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// embeddingRegistry 按名字取向量化实现。
type embeddingRegistry struct {
	conf   config.EmbeddingConfig
	byName map[string]interfaces.IEmbedding
}

// NewEmbeddingRegistry 启动时把每个 entry 造一个实现塞进 map。
//
// 不连网、不懒加载：配置错在这一步就暴露，而不是等某次导入跑到一半才炸。
func NewEmbeddingRegistry(conf *config.Config) (interfaces.IEmbeddingRegistry, error) {
	c := conf.Embedding

	r := &embeddingRegistry{
		conf:   c,
		byName: make(map[string]interfaces.IEmbedding, len(c.Models)),
	}
	for _, name := range c.Names() {
		p, err := c.Params(name)
		if err != nil {
			return nil, err
		}
		r.byName[name] = newEmbedding(p)
	}
	return r, nil
}

func (r *embeddingRegistry) Get(name string) (interfaces.IEmbedding, error) {
	if name == "" {
		name = r.conf.Default
	}
	impl, ok := r.byName[name]
	if !ok {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"没有名为 %q 的 embedding 模型。可用：%v", name, r.conf.Names()))
	}
	return impl, nil
}

func (r *embeddingRegistry) Names() []string { return r.conf.Names() }

// rerankRegistry 按名字取重排序实现。
type rerankRegistry struct {
	conf   config.RerankConfig
	byName map[string]interfaces.IRerank
}

// NewRerankRegistry 启动时把每个 entry 造一个实现塞进 map。
//
// enabled=false 时返回一个 byName 为空的注册表，它的 Get 对任何名字都给空实现——
// "这个实例要不要做重排"是部署决定，与"有哪几家可选"无关，两者不必合并（§10）。
func NewRerankRegistry(conf *config.Config) (interfaces.IRerankRegistry, error) {
	c := conf.Rerank

	r := &rerankRegistry{
		conf:   c,
		byName: make(map[string]interfaces.IRerank, len(c.Models)),
	}
	if !c.Enabled {
		return r, nil
	}

	for _, name := range c.Names() {
		p, err := c.Params(name)
		if err != nil {
			return nil, err
		}
		r.byName[name] = newRerank(p)
	}
	return r, nil
}

func (r *rerankRegistry) Get(name string) (interfaces.IRerank, error) {
	if !r.conf.Enabled {
		return nopRerank{}, nil
	}
	if name == "" {
		name = r.conf.Default
	}
	impl, ok := r.byName[name]
	if !ok {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"没有名为 %q 的 rerank 模型。可用：%v", name, r.conf.Names()))
	}
	return impl, nil
}

func (r *rerankRegistry) Names() []string { return r.conf.Names() }
