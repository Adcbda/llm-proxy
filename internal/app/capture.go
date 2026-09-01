package app

import (
	"bytes"
	"sync"
)

type LimitedCapture struct {
	mu        sync.RWMutex
	buffer    bytes.Buffer
	limit     int64
	total     int64
	truncated bool
}

func NewLimitedCapture(limit int64) *LimitedCapture {
	return &LimitedCapture{limit: limit}
}

func (capture *LimitedCapture) Write(data []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.total += int64(len(data))
	remaining := capture.limit - int64(capture.buffer.Len())
	if remaining > 0 {
		writeSize := int64(len(data))
		if writeSize > remaining {
			writeSize = remaining
		}
		_, _ = capture.buffer.Write(data[:writeSize])
	}
	if capture.total > capture.limit {
		capture.truncated = true
	}
	return len(data), nil
}

func (capture *LimitedCapture) Snapshot() ([]byte, int64, bool) {
	capture.mu.RLock()
	defer capture.mu.RUnlock()
	return bytes.Clone(capture.buffer.Bytes()), capture.total, capture.truncated
}

type ActiveCapture struct {
	ProjectID string
	Request   *LimitedCapture
	Response  *LimitedCapture
}

type ActiveCaptures struct {
	mu    sync.RWMutex
	items map[string]*ActiveCapture
}

func NewActiveCaptures() *ActiveCaptures {
	return &ActiveCaptures{items: make(map[string]*ActiveCapture)}
}

func (active *ActiveCaptures) Put(id string, capture *ActiveCapture) {
	active.mu.Lock()
	active.items[id] = capture
	active.mu.Unlock()
}

func (active *ActiveCaptures) Get(id string) (*ActiveCapture, bool) {
	active.mu.RLock()
	capture, ok := active.items[id]
	active.mu.RUnlock()
	return capture, ok
}

func (active *ActiveCaptures) Delete(id string) {
	active.mu.Lock()
	delete(active.items, id)
	active.mu.Unlock()
}
