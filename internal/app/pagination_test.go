package app

import (
	"net/http/httptest"
	"testing"
)

func TestFilePaginationAndSearch(t *testing.T) {
	items := []FileInfo{{Name: "alpha.txt"}, {Name: "beta.zip"}, {Name: "alpha-2.pdf"}, {Name: "gamma.iso"}}
	req := httptest.NewRequest("GET", "/api/files?paged=1&page=1&page_size=1&q=alpha", nil)
	page, size, keyword := parsePage(req)
	got := pageSlice(filterFiles(items, keyword), page, size)
	if got.Total != 2 || got.Pages != 2 || len(got.Items) != 1 || got.Items[0].Name != "alpha.txt" {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestHistoryPaginationFilters(t *testing.T) {
	items := []TransferRecord{
		{Direction: "send", Status: "completed", Name: "one.zip", Peer: "Office"},
		{Direction: "pull", Status: "completed", Name: "two.iso", Peer: "Mac"},
		{Direction: "send", Status: "completed", Name: "three.txt", Peer: "Mac"},
	}
	got := filterHistory(items, "mac", "send", "completed")
	if len(got) != 1 || got[0].Name != "three.txt" {
		t.Fatalf("unexpected filtered history: %+v", got)
	}
}
