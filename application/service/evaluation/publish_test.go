package evaluation

import (
	"fmt"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestQuickSelectionUsesOnlyApprovedAndBalancesBooks(t *testing.T) {
	items := []entity.EvalCandidate{}
	for index := 0; index < 20; index++ {
		book := "book-a"
		if index >= 10 {
			book = "book-b"
		}
		items = append(items, entity.EvalCandidate{
			ID: fmt.Sprint(index), DocID: book, Difficulty: "困难", Status: "approved",
		})
	}
	items = append(items, entity.EvalCandidate{ID: "unreviewed", Status: "draft"})

	selected, err := selectApprovedCandidates(items, 8)
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	seen := map[string]bool{}
	for _, item := range selected {
		if item.Status != "approved" || seen[item.ID] {
			t.Fatal("选入未审核或重复题")
		}
		seen[item.ID] = true
		counts[item.DocID]++
	}
	if len(selected) != 8 || counts["book-a"] != 4 || counts["book-b"] != 4 {
		t.Fatalf("选取不均衡: %+v", counts)
	}
	if _, err := selectApprovedCandidates(items, 21); err == nil {
		t.Fatal("用未审核题补足")
	}
}
