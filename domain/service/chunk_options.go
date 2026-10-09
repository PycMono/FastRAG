package service

import (
	"encoding/json"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// ChunkOptions 切片参数。
//
// 零值合法：表示「用默认值」，由 Normalize() 补齐。
type ChunkOptions struct {
	ChunkSize  int
	SplitLevel int
	MinChunk   int
}

// Normalize 把零值补成默认值，并做范围校验。
func (o ChunkOptions) Normalize() (ChunkOptions, error) {
	out := o
	if out.ChunkSize == 0 {
		out.ChunkSize = constants.DefaultChunkSize
	}
	if out.SplitLevel == 0 {
		out.SplitLevel = constants.DefaultSplitLevel
	}
	if out.MinChunk == 0 {
		out.MinChunk = constants.DefaultMinChunk
	}

	switch {
	case out.ChunkSize < 100 || out.ChunkSize > 4000:
		return out, apperrors.NewParamError("chunk_size 必须在 [100, 4000]")
	case out.SplitLevel < 1 || out.SplitLevel > 6:
		return out, apperrors.NewParamError("split_level 必须在 [1, 6]")
	case out.MinChunk < 0 || out.MinChunk*2 > out.ChunkSize:
		return out, apperrors.NewParamError("min_chunk 必须非负且不超过 chunk_size 的一半")
	}
	return out, nil
}

// Override 用非零字段覆盖当前值（请求参数覆盖 KB 上的快照）。
func (o ChunkOptions) Override(p *ChunkOptions) ChunkOptions {
	if p == nil {
		return o
	}
	out := o
	if p.ChunkSize > 0 {
		out.ChunkSize = p.ChunkSize
	}
	if p.SplitLevel > 0 {
		out.SplitLevel = p.SplitLevel
	}
	if p.MinChunk > 0 {
		out.MinChunk = p.MinChunk
	}
	return out
}

// ParseSplitOptions 解析 KB 上存的 split_options JSON 快照。
// 解析失败不报错——快照是历史数据，坏了就退回默认值，不该阻断导入。
func ParseSplitOptions(raw []byte) ChunkOptions {
	var opts ChunkOptions
	if len(raw) == 0 {
		return opts
	}
	// KB 上的快照用的是 snake_case key，与 DTO 一致
	var probe struct {
		ChunkSize  int `json:"chunk_size"`
		SplitLevel int `json:"split_level"`
		MinChunk   int `json:"min_chunk"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ChunkOptions{}
	}
	opts.ChunkSize = probe.ChunkSize
	opts.SplitLevel = probe.SplitLevel
	opts.MinChunk = probe.MinChunk
	return opts
}
