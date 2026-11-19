// Package events fans out change notifications to the web UI (server-sent events).
package events

import (
	"sync"
	"time"
)

// Event kinds.
const (
	ProjectCreated  = "project_created"
	ProjectDeleted  = "project_deleted"
	ProjectUpdated  = "project_updated" // description or params
	FileChanged     = "file_changed"
	RenderStarted   = "render_started"
	RenderDone      = "render_done"
	CommentsChanged = "comments_changed" // Detail says what happened, e.g. "replied to #3"
	ImagesChanged   = "images_changed"   // an upload or a delete; Detail says which
)

// Event is one change. Source is who made it ("web", or the agent's name).
type Event struct {
	Kind    string    `json:"kind"`
	Project string    `json:"project"`
	File    string    `json:"file,omitempty"`
	Source  string    `json:"source,omitempty"`
	OK      *bool     `json:"ok,omitempty"` // render_done
	Detail  string    `json:"detail,omitempty"`
	Time    time.Time `json:"time"`
}

// Hub broadcasts events to all subscribers; slow subscribers miss events rather than block.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
