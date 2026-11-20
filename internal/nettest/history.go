package nettest

import (
	"time"

	"github.com/neurorift/neurorift/internal/xjson"
)

// HistoryStore persists the most recent speed-test results (capped).
type HistoryStore struct {
	store *xjson.Store[historyDoc]
}

type historyDoc struct {
	Results []SpeedResult `json:"results"`
}

// NewHistoryStore opens the speed history at path.
func NewHistoryStore(path string) *HistoryStore {
	return &HistoryStore{store: xjson.NewStore[historyDoc](path)}
}

// Append records a result, keeping at most 200 entries.
func (h *HistoryStore) Append(r SpeedResult) {
	doc := h.store.LoadOrDefault(historyDoc{})
	doc.Results = append(doc.Results, r)
	if len(doc.Results) > 200 {
		doc.Results = doc.Results[len(doc.Results)-200:]
	}
	_ = h.store.Save(doc)
}

// Recent returns the latest result per node, newest first.
func (h *HistoryStore) Recent() map[string]SpeedResult {
	doc := h.store.LoadOrDefault(historyDoc{})
	out := map[string]SpeedResult{}
	for _, r := range doc.Results {
		if prev, ok := out[r.Node]; !ok || r.At.After(prev.At) {
			out[r.Node] = r
		}
	}
	return out
}

// List returns raw history (newest last).
func (h *HistoryStore) List(since time.Time) []SpeedResult {
	doc := h.store.LoadOrDefault(historyDoc{})
	out := []SpeedResult{}
	for _, r := range doc.Results {
		if r.At.After(since) {
			out = append(out, r)
		}
	}
	return out
}
