package websocket

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	gorilla "github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	maxRecent  = 50
	sendQueue  = 64
	readLimit  = 4096
)

// Hub fans outbound events out to every connected browser.
type Hub struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
	recent  [][]byte
	closed  bool
	logger  *slog.Logger
}

// NewHub returns an empty hub. A nil logger discards log records.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Hub{
		clients: make(map[*Client]struct{}),
		logger:  logger,
	}
}

// Broadcast marshals msg and delivers it to every client.
// Slow clients drop the message instead of blocking the caller.
func (h *Hub) Broadcast(msg Outbound) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("marshal outbound", "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if msg.Event != "clients" {
		h.rememberLocked(data)
	}
	for c := range h.clients {
		c.trySend(data)
	}
}

// ClientCount reports how many browsers are connected.
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Close disconnects every client. It is safe to call more than once.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
		c.closeSend()
	}
	h.clients = map[*Client]struct{}{}
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.conn.Close()
	}
}

func (h *Hub) rememberLocked(data []byte) {
	if len(h.recent) == maxRecent {
		h.recent = h.recent[1:]
	}
	h.recent = append(h.recent, data)
}

func (h *Hub) add(c *Client) (recent [][]byte, n int, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, 0, false
	}
	h.clients[c] = struct{}{}
	recent = make([][]byte, len(h.recent))
	for i, ev := range h.recent {
		recent[i] = ev
	}
	return recent, len(h.clients), true
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	_, ok := h.clients[c]
	if ok {
		delete(h.clients, c)
		c.closeSend()
	}
	n := len(h.clients)
	closed := h.closed
	h.mu.Unlock()
	if ok && !closed {
		h.Broadcast(Outbound{Event: "clients", Data: itoa(n)})
	}
}

// Client is one WebSocket connection.
type Client struct {
	hub  *Hub
	conn *gorilla.Conn
	send chan []byte

	mu     sync.Mutex
	closed bool
}

func newClient(conn *gorilla.Conn) *Client {
	return &Client{
		conn: conn,
		send: make(chan []byte, sendQueue),
	}
}

func (c *Client) trySend(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	cloned := make([]byte, len(data))
	copy(cloned, data)
	select {
	case c.send <- cloned:
	default:
	}
}

func (c *Client) closeSend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.send)
}

func (c *Client) readPump(s *Server) {
	defer func() {
		s.hub.remove(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(readLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if gorilla.IsUnexpectedCloseError(err, gorilla.CloseGoingAway, gorilla.CloseNormalClosure, gorilla.CloseNoStatusReceived) {
				s.logger.Debug("websocket read", "err", err)
			}
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		s.handleInbound(c, data)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(gorilla.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(gorilla.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(gorilla.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
