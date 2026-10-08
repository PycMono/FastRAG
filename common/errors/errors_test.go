package errors

import (
	"errors"
	"fmt"
	"testing"
)

func TestBizError_CodeAndMessage(t *testing.T) {
	err := NewBizError(CodeInvalidParam, "invalid parameter")

	if err.Code() != CodeInvalidParam {
		t.Fatalf("Code() = %d, want %d", err.Code(), CodeInvalidParam)
	}
	if err.Message() != "invalid parameter" {
		t.Fatalf("Message() = %q, want %q", err.Message(), "invalid parameter")
	}
	if err.Error() != "invalid parameter" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "invalid parameter")
	}
}

func TestCodeError_IsMatchesByCode(t *testing.T) {
	err := ErrInvalidParam.Params("name")

	if !errors.Is(err, ErrInvalidParam) {
		t.Fatal("expected errors.Is(err, ErrInvalidParam) to be true (same code)")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("expected errors.Is(err, ErrNotFound) to be false (different code)")
	}
}

func TestBizError_ParamsKeepsCodeAndDoesNotMutateSource(t *testing.T) {
	base := NewBizError(10999, "user %s not found")
	filled := base.Params("u1")

	if filled.Code() != 10999 {
		t.Fatalf("filled.Code() = %d, want 10999", filled.Code())
	}
	if filled.Message() != "user u1 not found" {
		t.Fatalf("filled.Message() = %q, want %q", filled.Message(), "user u1 not found")
	}
	if base.Message() != "user %s not found" {
		t.Fatalf("source error was mutated: %q", base.Message())
	}
}

func TestCodeError_WrapUnwrapsToCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := ErrKnowledgeBaseCreateFailed.Wrap(cause)

	if !errors.Is(err, cause) {
		t.Fatal("expected errors.Is(err, cause) to be true after Wrap")
	}
	if err.Code() != CodeKnowledgeBaseCreateFail {
		t.Fatalf("Code() = %d, want %d", err.Code(), CodeKnowledgeBaseCreateFail)
	}
}

func TestAsBizError_FindsWrappedBizError(t *testing.T) {
	wrapped := fmt.Errorf("bind failed: %w", ErrInvalidParam)

	biz, ok := AsBizError(wrapped)
	if !ok {
		t.Fatal("expected AsBizError to find the wrapped BizError")
	}
	if biz.Code() != CodeInvalidParam {
		t.Fatalf("Code() = %d, want %d", biz.Code(), CodeInvalidParam)
	}
}

func TestAsBizError_ReturnsFalseForSysError(t *testing.T) {
	if _, ok := AsBizError(ErrInternal); ok {
		t.Fatal("expected AsBizError to be false for a SysError")
	}
}

func TestAsSysError_FindsWrappedSysError(t *testing.T) {
	wrapped := fmt.Errorf("query failed: %w", ErrInternal)

	sys, ok := AsSysError(wrapped)
	if !ok {
		t.Fatal("expected AsSysError to find the wrapped SysError")
	}
	if sys.Code() != CodeInternal {
		t.Fatalf("Code() = %d, want %d", sys.Code(), CodeInternal)
	}
}

func TestAsSysError_ReturnsFalseForBizError(t *testing.T) {
	if _, ok := AsSysError(ErrNotFound); ok {
		t.Fatal("expected AsSysError to be false for a BizError")
	}
}
