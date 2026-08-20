package app

import (
	"sort"
	"sync"
	"time"
)

type TransferRecord struct {
	ID             string  `json:"id"`
	Direction      string  `json:"direction"`
	Peer           string  `json:"peer"`
	Name           string  `json:"name"`
	Size           int64   `json:"size"`
	StartedAt      int64   `json:"started_at"`
	FinishedAt     int64   `json:"finished_at"`
	Status         string  `json:"status"`
	BytesPerSecond float64 `json:"bytes_per_second"`
	Encrypted      bool    `json:"encrypted"`
	Resumed        bool    `json:"resumed"`
}

type historyStore struct {
	mu      sync.RWMutex
	records []TransferRecord
	max     int
}

func newHistory(max int) *historyStore { return &historyStore{max: max} }

func (h *historyStore) add(r TransferRecord) {
	if r.ID == "" {
		r.ID = randomID()
	}
	if r.FinishedAt == 0 {
		r.FinishedAt = time.Now().Unix()
	}
	h.mu.Lock()
	h.records = append(h.records, r)
	if len(h.records) > h.max {
		h.records = append([]TransferRecord(nil), h.records[len(h.records)-h.max:]...)
	}
	h.mu.Unlock()
}

func (h *historyStore) list() []TransferRecord {
	h.mu.RLock()
	out := append([]TransferRecord(nil), h.records...)
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedAt > out[j].FinishedAt })
	return out
}
