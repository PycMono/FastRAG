package service

import (
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
