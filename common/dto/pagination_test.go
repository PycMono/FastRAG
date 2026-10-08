package dto

import "testing"

func TestPageQuery_OffsetAndLimit(t *testing.T) {
	q := PageQuery{Page: 3, PageSize: 10}

	if q.Offset() != 20 {
		t.Fatalf("Offset() = %d, want 20", q.Offset())
	}
	if q.Limit() != 10 {
		t.Fatalf("Limit() = %d, want 10", q.Limit())
	}
}

func TestPageQuery_NormalizesInvalidValues(t *testing.T) {
	q := PageQuery{Page: 0, PageSize: 0}

	if q.Offset() != 0 {
		t.Fatalf("Offset() = %d, want 0", q.Offset())
	}
	if q.Limit() != 10 {
		t.Fatalf("Limit() = %d, want 10 (default)", q.Limit())
	}

	big := PageQuery{Page: 1, PageSize: 500}
	if big.Limit() != 100 {
		t.Fatalf("Limit() = %d, want 100 (capped)", big.Limit())
	}
}

func TestPageQuery_OffsetUsesNormalizedLimit(t *testing.T) {
	// PageSize 超上限时，Offset 必须按归一化后的 100 计算，否则分页会错位
	q := PageQuery{Page: 2, PageSize: 500}

	if q.Offset() != 100 {
		t.Fatalf("Offset() = %d, want 100", q.Offset())
	}
}
