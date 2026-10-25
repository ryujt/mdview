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

// Client는 단일 WebSocket 연결을 표현한다.
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub는 다수의 WebSocket 클라이언트를 관리하고 브로드캐스트하는 Worker이다.
// 폴더 모드에서는 현재 활성 파일 경로(절대 경로)를 함께 관리한다.
type Hub struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte

	mu         sync.RWMutex
	activeFile string // folder 모드에서의 현재 활성 파일 절대 경로

	// onActiveChange는 활성 파일이 바뀔 때 호출된다(folder 모드).
	// Orchestrator가 주입하여 watcher 감시 대상을 활성 파일로만 좁히는 데 쓴다.
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

// Run은 등록/해제/브로드캐스트 이벤트를 처리하는 이벤트 루프이다.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			logging.Debug("[Hub] 클라이언트 등록 (총 %d)", len(h.clients))
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				logging.Debug("[Hub] 클라이언트 해제 (총 %d)", len(h.clients))
			}
		case msg := <-h.broadcast:
			logging.Debug("[Hub] 브로드캐스트: %d bytes → %d clients", len(msg), len(h.clients))
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					close(c.send)
					delete(h.clients, c)
					logging.Warn("[Hub] 송신 버퍼 포화로 클라이언트 강제 해제")
				}
			}
		}
	}
}

// Broadcast는 모든 연결된 클라이언트에 메시지를 전송한다.
func (h *Hub) Broadcast(msg []byte) {
	h.broadcast <- msg
}

// Register는 신규 클라이언트를 등록한다.
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister는 클라이언트 등록을 해제한다.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// SetActiveFile는 folder 모드에서의 활성 파일(절대 경로)을 설정한다.
func (h *Hub) SetActiveFile(absPath string) {
	h.mu.Lock()
	h.activeFile = absPath
	cb := h.onActiveChange
	h.mu.Unlock()
	logging.Debug("[Hub] 활성 파일 변경: %s", absPath)
	if cb != nil {
		cb(absPath)
	}
}

// SetOnActiveChange는 활성 파일 변경 콜백을 등록한다(folder 모드).
func (h *Hub) SetOnActiveChange(cb func(absPath string)) {
	h.mu.Lock()
	h.onActiveChange = cb
	h.mu.Unlock()
}

// ActiveFile은 folder 모드에서의 활성 파일(절대 경로)을 반환한다.
func (h *Hub) ActiveFile() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.activeFile
}
