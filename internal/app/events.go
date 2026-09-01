package app

import (
	"encoding/json"
	"sync"
	"time"
)

type LiveEvent struct {
	Type      string    `json:"type"`
	ProjectID string    `json:"projectId"`
	RequestID string    `json:"requestId,omitempty"`
	At        time.Time `json:"at"`
}

type EventHub struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]chan LiveEvent
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[int]chan LiveEvent)}
}

func (hub *EventHub) Subscribe() (int, <-chan LiveEvent) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextID++
	channel := make(chan LiveEvent, 32)
	hub.subscribers[hub.nextID] = channel
	return hub.nextID, channel
}

func (hub *EventHub) Unsubscribe(id int) {
	hub.mu.Lock()
	if channel, ok := hub.subscribers[id]; ok {
		delete(hub.subscribers, id)
		close(channel)
	}
	hub.mu.Unlock()
}

func (hub *EventHub) Publish(event LiveEvent) {
	event.At = time.Now().UTC()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, channel := range hub.subscribers {
		select {
		case channel <- event:
		default:
		}
	}
}

func (event LiveEvent) JSON() []byte {
	data, _ := json.Marshal(event)
	return data
}
