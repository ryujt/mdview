package mdview

import (
	"sync"

	"github.com/gorilla/websocket"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

const (
	clientSendBuffer = 256
	hubEventBuffer   = 256
)

// Client represents a single WebSocket connection.
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub is the Worker that manages WebSocket clients and broadcasts to them.
// In folder mode it also tracks the current active file path (absolute).
type Hub struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte

	mu         sync.RWMutex
	activeFile string // absolute path of the current active file in folder mode

	// onActiveChange is called when the active file changes (folder mode).
	// Injected by the Orchestrator to narrow the watcher to the active file only.
	onActiveChange func(absPath string)
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, hubEventBuffer),
	}
}

// Run is the event loop handling register/unregister/broadcast events.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			logging.Debug("[Hub] client registered (total %d)", len(h.clients))
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				logging.Debug("[Hub] client unregistered (total %d)", len(h.clients))
			}
		case msg := <-h.broadcast:
			logging.Debug("[Hub] broadcast: %d bytes → %d clients", len(msg), len(h.clients))
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					close(c.send)
					delete(h.clients, c)
					logging.Warn("[Hub] send buffer full; dropping client")
				}
			}
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg []byte) {
	h.broadcast <- msg
}

// Register registers a new client.
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister unregisters a client.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// SetActiveFile sets the active file (absolute path) in folder mode.
func (h *Hub) SetActiveFile(absPath string) {
	h.mu.Lock()
	h.activeFile = absPath
	cb := h.onActiveChange
	h.mu.Unlock()
	logging.Debug("[Hub] active file changed: %s", absPath)
	if cb != nil {
		cb(absPath)
	}
}

// SetOnActiveChange registers the active-file change callback (folder mode).
func (h *Hub) SetOnActiveChange(cb func(absPath string)) {
	h.mu.Lock()
	h.onActiveChange = cb
	h.mu.Unlock()
}

// ActiveFile returns the active file (absolute path) in folder mode.
func (h *Hub) ActiveFile() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.activeFile
}
