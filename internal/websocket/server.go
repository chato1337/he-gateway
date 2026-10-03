package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	gorilla "github.com/gorilla/websocket"
)

// Hardware is the gateway's view of local serial devices.
// The concrete manager lives in the hardware package and is injected from main.
type Hardware interface {
	ListPorts(ctx context.Context) ([]string, error)
	Listening() []string
	OpenDoor(ctx context.Context, deviceID string) error
	StartListen(ctx context.Context, deviceID string) error
	StopListen(ctx context.Context, deviceID string) error
	Write(ctx context.Context, deviceID string, payload []byte) error
}

// Status is the JSON body of GET /api/status.
type Status struct {
	Clients   int      `json:"clients"`
	Listening []string `json:"listening"`
}

// Server serves the dashboard, the JSON API, and the WebSocket endpoint.
type Server struct {
	hub      *Hub
	hw       Hardware
	index    []byte
	logger   *slog.Logger
	upgrader gorilla.Upgrader
	timeout  time.Duration
}

// NewServer wires the hub, the hardware controller, and the embedded dashboard.
// Local browsers on other origins (the cloud React app) are accepted because
// the process binds to loopback by default.
func NewServer(hub *Hub, hw Hardware, index []byte, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if hub == nil {
		hub = NewHub(logger)
	}
	return &Server{
		hub:     hub,
		hw:      hw,
		index:   index,
		logger:  logger,
		timeout: 5 * time.Second,
		upgrader: gorilla.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

// Handler returns the HTTP routes for the gateway.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET /ws", s.serveWS)
	mux.HandleFunc("GET /api/ports", s.servePorts)
	mux.HandleFunc("GET /api/status", s.serveStatus)
	return mux
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	if len(s.index) == 0 {
		http.Error(w, "dashboard not embedded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(s.index)
}

func (s *Server) servePorts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	ports, err := s.hw.ListPorts(ctx)
	if err != nil {
		s.logger.Error("list ports", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if ports == nil {
		ports = []string{}
	}
	writeJSON(w, http.StatusOK, ports)
}

func (s *Server) serveStatus(w http.ResponseWriter, _ *http.Request) {
	listening := s.hw.Listening()
	if listening == nil {
		listening = []string{}
	}
	writeJSON(w, http.StatusOK, Status{
		Clients:   s.hub.ClientCount(),
		Listening: listening,
	})
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("websocket upgrade", "err", err)
		return
	}
	c := newClient(conn)
	recent, n, ok := s.hub.add(c)
	if !ok {
		_ = conn.Close()
		return
	}
	s.logger.Info("websocket connected", "clients", n)
	hello, err := json.Marshal(Outbound{Event: "hello", Data: itoa(n)})
	if err != nil {
		s.logger.Error("marshal hello", "err", err)
	} else {
		c.trySend(hello)
	}
	for _, ev := range recent {
		c.trySend(ev)
	}
	go c.writePump()
	s.hub.Broadcast(Outbound{Event: "clients", Data: itoa(n)})
	c.readPump(s)
}

func (s *Server) handleInbound(c *Client, data []byte) {
	msg, err := ParseInbound(data)
	if err != nil {
		s.logger.Warn("invalid message", "err", err)
		s.reply(c, Outbound{Event: "error", Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	switch msg.Action {
	case ActionListPorts:
		ports, err := s.hw.ListPorts(ctx)
		if err != nil {
			s.logger.Error("list ports", "err", err)
			s.reply(c, Outbound{Event: "error", Action: msg.Action, Error: err.Error()})
			return
		}
		if ports == nil {
			ports = []string{}
		}
		s.reply(c, Outbound{Event: "ports", Action: msg.Action, Ports: ports})
	case ActionOpenDoor:
		if err := s.hw.OpenDoor(ctx, msg.DeviceID); err != nil {
			s.logger.Error("open door", "device_id", msg.DeviceID, "err", err)
			s.reply(c, Outbound{Event: "error", Action: msg.Action, DeviceID: msg.DeviceID, Error: err.Error()})
			return
		}
		s.logger.Info("door opened", "device_id", msg.DeviceID)
		s.hub.Broadcast(Outbound{Event: "door_opened", Action: msg.Action, DeviceID: msg.DeviceID})
	case ActionListen:
		if err := s.hw.StartListen(ctx, msg.DeviceID); err != nil {
			s.logger.Error("start listen", "device_id", msg.DeviceID, "err", err)
			s.reply(c, Outbound{Event: "error", Action: msg.Action, DeviceID: msg.DeviceID, Error: err.Error()})
			return
		}
		s.logger.Info("listening", "device_id", msg.DeviceID)
		s.hub.Broadcast(Outbound{Event: "listening", Action: msg.Action, DeviceID: msg.DeviceID})
	case ActionStopListen:
		if err := s.hw.StopListen(ctx, msg.DeviceID); err != nil {
			s.logger.Error("stop listen", "device_id", msg.DeviceID, "err", err)
			s.reply(c, Outbound{Event: "error", Action: msg.Action, DeviceID: msg.DeviceID, Error: err.Error()})
			return
		}
		s.logger.Info("listen stopped", "device_id", msg.DeviceID)
		s.hub.Broadcast(Outbound{Event: "listen_stopped", Action: msg.Action, DeviceID: msg.DeviceID})
	case ActionWrite:
		if err := s.hw.Write(ctx, msg.DeviceID, []byte(msg.Payload)); err != nil {
			s.logger.Error("write", "device_id", msg.DeviceID, "err", err)
			s.reply(c, Outbound{Event: "error", Action: msg.Action, DeviceID: msg.DeviceID, Error: err.Error()})
			return
		}
		s.hub.Broadcast(Outbound{Event: "written", Action: msg.Action, DeviceID: msg.DeviceID, Data: msg.Payload})
	default:
		s.reply(c, Outbound{Event: "error", Action: msg.Action, Error: fmt.Sprintf("unsupported action %q", msg.Action)})
	}
}

func (s *Server) reply(c *Client, msg Outbound) {
	data, err := json.Marshal(msg)
	if err != nil {
		s.logger.Error("marshal reply", "err", err)
		return
	}
	c.trySend(data)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
