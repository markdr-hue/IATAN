package public

import (
	"log/slog"
	"sync"
)

// wsHub manages WebSocket rooms. Each room is identified by a key
// formatted as "siteID:endpointPath:room" ensuring full site isolation.
type wsHub struct {
	mu    sync.RWMutex
	rooms map[string]map[string]*wsClient // roomKey → clientID → client
}

// wsClient represents a single WebSocket connection in a room.
type wsClient struct {
	id string
	ch chan []byte // buffered outbound messages
}

// NewWSHub creates a new WebSocket hub for managing rooms.
func NewWSHub() *wsHub {
	return &wsHub{
		rooms: make(map[string]map[string]*wsClient),
	}
}

// join adds a client to a room and returns the client handle.
func (h *wsHub) join(roomKey, clientID string) *wsClient {
	c := &wsClient{
		id: clientID,
		ch: make(chan []byte, 64),
	}
	h.mu.Lock()
	room, ok := h.rooms[roomKey]
	if !ok {
		room = make(map[string]*wsClient)
		h.rooms[roomKey] = room
	}
	room[clientID] = c
	h.mu.Unlock()
	return c
}

// leave removes a client from a room and cleans up empty rooms.
func (h *wsHub) leave(roomKey, clientID string) {
	h.mu.Lock()
	if room, ok := h.rooms[roomKey]; ok {
		delete(room, clientID)
		if len(room) == 0 {
			delete(h.rooms, roomKey)
		}
	}
	h.mu.Unlock()
}

// broadcast sends msg to all clients in the room except the sender.
func (h *wsHub) broadcast(roomKey, senderID string, msg []byte) {
	h.mu.RLock()
	room := h.rooms[roomKey]
	if room == nil {
		h.mu.RUnlock()
		return
	}
	// Snapshot the clients under read lock.
	targets := make([]*wsClient, 0, len(room))
	for _, c := range room {
		if c.id != senderID {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.ch <- msg:
		default:
			slog.Warn("ws: message dropped (channel full)", "room", roomKey, "client", c.id)
		}
	}
}
