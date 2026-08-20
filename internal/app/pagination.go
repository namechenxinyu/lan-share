package app

import (
	"net/http"
	"strconv"
	"strings"
)

type PageResult[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Pages    int `json:"pages"`
}

func parsePage(r *http.Request) (page, pageSize int, keyword string) {
	page = 1
	pageSize = 10
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && n > 0 {
		pageSize = n
	}
	if pageSize > 100 {
		pageSize = 100
	}
	keyword = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	return
}

func pageSlice[T any](items []T, page, pageSize int) PageResult[T] {
	total := len(items)
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if page < 1 {
		page = 1
	}
	if pages > 0 && page > pages {
		page = pages
	}
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := append([]T(nil), items[start:end]...)
	return PageResult[T]{Items: out, Total: total, Page: page, PageSize: pageSize, Pages: pages}
}

func filterFiles(items []FileInfo, keyword string) []FileInfo {
	if keyword == "" {
		return items
	}
	out := make([]FileInfo, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name), keyword) {
			out = append(out, item)
		}
	}
	return out
}

func filterHistory(items []TransferRecord, keyword, direction, status string) []TransferRecord {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	direction = strings.ToLower(strings.TrimSpace(direction))
	status = strings.ToLower(strings.TrimSpace(status))
	if keyword == "" && direction == "" && status == "" {
		return items
	}
	out := make([]TransferRecord, 0, len(items))
	for _, item := range items {
		if direction != "" && strings.ToLower(item.Direction) != direction {
			continue
		}
		if status != "" && strings.ToLower(item.Status) != status {
			continue
		}
		if keyword != "" {
			hay := strings.ToLower(item.Name + " " + item.Peer)
			if !strings.Contains(hay, keyword) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}
