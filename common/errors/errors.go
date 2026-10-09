package errors

import (
	"errors"
	"fmt"
)

// ─── 错误码常量 ──────────────────────────────────────────────────────────────

// 通用错误码
const (
	CodeInvalidParam = 10001 // 参数错误
	CodeUnauthorized = 10002 // 未登录
	CodeForbidden    = 10003 // 无权限
	CodeNotFound     = 10004 // 资源不存在
	CodeConflict     = 10005 // 资源冲突
	CodeRateLimited  = 10006 // 触发限流
	CodeInternal     = 10007 // 服务内部错误
)

// knowledge 模块错误码
const (
	CodeKnowledgeBaseNotFound     = 10101 // 知识库不存在
	CodeKnowledgeBaseNameExists   = 10102 // 知识库名称已存在
	CodeKnowledgeBaseCreateFail   = 10103 // 知识库创建失败
	CodeKnowledgeBaseUpdateFail   = 10104 // 知识库更新失败
	CodeKnowledgeBaseDeleteFail   = 10105 // 知识库删除失败
	CodeKnowledgeRetrievalNotImpl = 10106 // 检索能力未实现（已被下方 10301 取代，仅保留兼容）
)

// 导入相关错误码
const (
	CodeDocChunkEmpty   = 10201 // 切片结果为空
	CodeChunkTooLarge   = 10202 // 单切片过长
	CodeDocIngestFail   = 10203 // 导入失败
	CodeDocDeleteFail   = 10204 // 文档删除失败
	CodeEmbeddingFail   = 10205 // embedding 服务异常
	CodeVectorStoreFail = 10206 // 向量库异常
	CodeIndexInitFail   = 10207 // 索引初始化失败
)

// 检索相关错误码
const (
	CodeSearchFail = 10301 // 检索失败
)

// ─── CodeError 基础 ──────────────────────────────────────────────────────────

// CodeError 带错误码的错误接口
type CodeError interface {
	error
	Code() int
	Message() string
	Unwrap() error
}

// codeError 基础实现
type codeError struct {
	code  int
	msg   string
	cause error
}

func newCodeError(code int, msg string, cause error) *codeError {
	return &codeError{
		code:  code,
		msg:   msg,
		cause: cause,
	}
}

func (e *codeError) Code() int       { return e.code }
func (e *codeError) Message() string { return e.msg }
func (e *codeError) Error() string   { return e.msg }
func (e *codeError) Unwrap() error   { return e.cause }

// Is 按 code 判断是否是同一个错误类型
func (e *codeError) Is(target error) bool {
	if ce, ok := target.(CodeError); ok {
		return ce.Code() == e.code
	}
	return errors.Is(e.cause, target)
}

// Params 用格式化参数生成新的错误（保留原 code，不修改原错误）
func (e *codeError) Params(args ...interface{}) *codeError {
	return newCodeError(e.code, fmt.Sprintf(e.msg, args...), e.cause)
}

// Wrap 包装底层错误
func (e *codeError) Wrap(err error) *codeError {
	return newCodeError(e.code, e.msg, err)
}

// ─── BizError 业务错误（预期内）──────────────────────────────────────────────

// BizError 业务错误：由业务规则触发，属于预期内的失败
type BizError struct{ *codeError }

// NewBizError 创建业务错误
func NewBizError(code int, msg string) *BizError {
	return &BizError{newCodeError(code, msg, nil)}
}

// Wrap 包装底层错误
func (e *BizError) Wrap(err error) *BizError {
	return &BizError{e.codeError.Wrap(err)}
}

// Params 用格式化参数生成新的业务错误
func (e *BizError) Params(args ...interface{}) *BizError {
	return &BizError{e.codeError.Params(args...)}
}

// AsBizError 从错误链中提取 BizError
func AsBizError(err error) (*BizError, bool) {
	var biz *BizError
	if errors.As(err, &biz) {
		return biz, true
	}
	return nil, false
}

// ─── SysError 系统错误（非预期）──────────────────────────────────────────────

// SysError 系统错误：基础设施/依赖异常导致，属于非预期失败
type SysError struct{ *codeError }

// NewSysError 创建系统错误
func NewSysError(code int, msg string) *SysError {
	return &SysError{newCodeError(code, msg, nil)}
}

// Wrap 包装底层错误
func (e *SysError) Wrap(err error) *SysError {
	return &SysError{e.codeError.Wrap(err)}
}

// Params 用格式化参数生成新的系统错误
func (e *SysError) Params(args ...interface{}) *SysError {
	return &SysError{e.codeError.Params(args...)}
}

// AsSysError 从错误链中提取 SysError
func AsSysError(err error) (*SysError, bool) {
	var sys *SysError
	if errors.As(err, &sys) {
		return sys, true
	}
	return nil, false
}

// ─── 通用错误 ────────────────────────────────────────────────────────────────

var (
	ErrInvalidParam = NewBizError(CodeInvalidParam, "invalid parameter")
	ErrUnauthorized = NewBizError(CodeUnauthorized, "unauthorized")
	ErrForbidden    = NewBizError(CodeForbidden, "forbidden")
	ErrNotFound     = NewBizError(CodeNotFound, "resource not found")
	ErrConflict     = NewBizError(CodeConflict, "resource conflict")
	ErrRateLimited  = NewBizError(CodeRateLimited, "rate limited")
	ErrInternal     = NewSysError(CodeInternal, "internal server error")
)

// ─── Knowledge 模块 ───────────────────────────────────────────────────────────

var (
	ErrKnowledgeBaseNotFound     = NewBizError(CodeKnowledgeBaseNotFound, "knowledge base not found")
	ErrKnowledgeBaseNameExists   = NewBizError(CodeKnowledgeBaseNameExists, "knowledge base name already exists")
	ErrKnowledgeBaseCreateFailed = NewSysError(CodeKnowledgeBaseCreateFail, "knowledge base create failed")
	ErrKnowledgeBaseUpdateFailed = NewSysError(CodeKnowledgeBaseUpdateFail, "knowledge base update failed")
	ErrKnowledgeBaseDeleteFailed = NewSysError(CodeKnowledgeBaseDeleteFail, "knowledge base delete failed")
	ErrKnowledgeRetrievalNotImpl = NewBizError(CodeKnowledgeRetrievalNotImpl, "knowledge retrieval not implemented")
)

// ─── 文档导入 / 检索 ──────────────────────────────────────────────────────────

var (
	ErrDocChunkEmpty     = NewBizError(CodeDocChunkEmpty, "chunk result is empty")
	ErrChunkTooLarge     = NewBizError(CodeChunkTooLarge, "chunk too large for embedding")
	ErrDocIngestFailed   = NewSysError(CodeDocIngestFail, "doc ingest failed")
	ErrDocDeleteFailed   = NewSysError(CodeDocDeleteFail, "doc delete failed")
	ErrEmbeddingFailed   = NewSysError(CodeEmbeddingFail, "embedding service failed")
	ErrVectorStoreFailed = NewSysError(CodeVectorStoreFail, "vector store failed")
	ErrIndexInitFailed   = NewSysError(CodeIndexInitFail, "index init failed")
	ErrSearchFailed      = NewSysError(CodeSearchFail, "search failed")
)

// NewParamError 生成带自定义文案的参数错误。
//
// 基础错误 ErrInvalidParam 的 msg 是固定串，Params() 走的是 Sprintf，
// 没有占位符时参数会被直接丢掉，所以另开一个构造函数。
func NewParamError(msg string) *BizError {
	return NewBizError(CodeInvalidParam, msg)
}
