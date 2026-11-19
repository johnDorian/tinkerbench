// Package events fans out change notifications to the web UI as server-sent events.
package events

import (
	"encoding/json"
	"sync"
)

// Event is one notification: {"type": ..., "id": ..., plus the fields of the changed object}.
// Types: plate (id), slice (id, the SliceResult fields), models (id), printer (the Printer fields).
type Event map[string]any

// Merge builds an event from a type, an id (may be empty) and a value whose JSON fields are
// added to the event.
func Merge(kind, id string, v any) Event {
	e := Event{"type": kind}
	if id != "" {
		e["id"] = id
	}
	if v == nil {
		return e
	}
	b, err := json.Marshal(v)
	if err != nil {
		return e
	}
	var fields map[string]any
	if json.Unmarshal(b, &fields) == nil {
		for k, val := range fields {
			if k != "type" && k != "id" {
				e[k] = val
			}
		}
	}
	return e
}

// Hub broadcasts events to all subscribers. A slow subscriber misses events rather than block.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events; call Unsubscribe when done.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe stops delivery to ch.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Subscribers returns how many streams are open.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Publish sends e to every subscriber.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
