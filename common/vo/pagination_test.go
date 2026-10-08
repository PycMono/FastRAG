package vo

import "testing"

func TestNewPageResult_ExactDivision(t *testing.T) {
	r := NewPageResult(20, []string{"a", "b"}, 1, 10)

	if r.Total != 20 {
		t.Fatalf("Total = %d, want 20", r.Total)
	}
	if r.Pages != 2 {
		t.Fatalf("Pages = %d, want 2", r.Pages)
	}
	if r.Page != 1 || r.PageSize != 10 {
		t.Fatalf("Page/PageSize = %d/%d, want 1/10", r.Page, r.PageSize)
	}
	if len(r.List) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(r.List))
	}
}

func TestNewPageResult_RemainderRoundsUp(t *testing.T) {
	r := NewPageResult(25, []string{}, 3, 10)

	if r.Pages != 3 {
		t.Fatalf("Pages = %d, want 3", r.Pages)
	}
}

func TestNewPageResult_ZeroTotalStillOnePage(t *testing.T) {
	r := NewPageResult(0, []string{}, 1, 10)

	if r.Total != 0 {
		t.Fatalf("Total = %d, want 0", r.Total)
	}
	if r.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", r.Pages)
	}
}

func TestNewPageResult_TotalSmallerThanPageSize(t *testing.T) {
	r := NewPageResult(3, []string{"a"}, 1, 10)

	if r.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", r.Pages)
	}
}

func TestNewPageResult_ZeroPageSizeDefaultsInsteadOfPanicking(t *testing.T) {
	r := NewPageResult(0, []string{}, 1, 0)

	if r.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", r.Pages)
	}
	if r.PageSize != 10 {
		t.Fatalf("PageSize = %d, want 10 (default)", r.PageSize)
	}
}
