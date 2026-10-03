package tests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	ws "he-gateway/internal/websocket"
	"he-gateway/web"
)

func TestParseInbound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    ws.Inbound
		wantErr string
	}{
		{
			name: "open door",
			raw:  `{"action":"open_door","device_id":"COM3"}`,
			want: ws.Inbound{Action: ws.ActionOpenDoor, DeviceID: "COM3"},
		},
		{
			name: "trimmed",
			raw:  `{"action":" listen ","device_id":" /dev/ttyUSB0 "}`,
			want: ws.Inbound{Action: ws.ActionListen, DeviceID: "/dev/ttyUSB0"},
		},
		{
			name: "list ports",
			raw:  `{"action":"list_ports"}`,
			want: ws.Inbound{Action: ws.ActionListPorts},
		},
		{
			name: "write",
			raw:  `{"action":"write","device_id":"COM3","payload":"PING"}`,
			want: ws.Inbound{Action: ws.ActionWrite, DeviceID: "COM3", Payload: "PING"},
		},
		{name: "empty", raw: ``, wantErr: "empty payload"},
		{name: "blank", raw: "  \n", wantErr: "empty payload"},
		{name: "invalid json", raw: `{`, wantErr: "parse message"},
		{name: "missing action", raw: `{"device_id":"COM3"}`, wantErr: "action is required"},
		{name: "unknown action", raw: `{"action":"explode"}`, wantErr: "unknown action"},
		{name: "missing device", raw: `{"action":"open_door"}`, wantErr: "device_id is required"},
		{name: "device newline", raw: `{"action":"open_door","device_id":"CO\nM3"}`, wantErr: "invalid characters"},
		{name: "write without payload", raw: `{"action":"write","device_id":"COM3"}`, wantErr: "payload is required"},
		{name: "extra value", raw: `{"action":"list_ports"}{"action":"list_ports"}`, wantErr: "extra data"},
		{name: "trailing text", raw: `{"action":"list_ports"} nope`, wantErr: "parse message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ws.ParseInbound([]byte(tt.raw))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOutboundOmitsEmptyPorts(t *testing.T) {
	t.Parallel()
	hello, err := json.Marshal(ws.Outbound{Event: "hello", Data: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hello), "ports") {
		t.Fatalf("hello = %s", hello)
	}
	ports, err := json.Marshal(ws.Outbound{Event: "ports", Ports: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ports), `"ports":[]`) {
		t.Fatalf("ports = %s", ports)
	}
}

func TestWebSocketOpenDoor(t *testing.T) {
	t.Parallel()
	hw := &fakeHardware{}
	ts, hub := newServer(t, hw)
	defer ts.Close()
	defer hub.Close()
	conn := dial(t, ts)
	defer conn.Close()

	hello := readUntil(t, conn, "hello")
	if hello.Data != "1" {
		t.Fatalf("hello = %+v", hello)
	}
	if err := conn.WriteJSON(ws.Inbound{Action: ws.ActionOpenDoor, DeviceID: "COM3"}); err != nil {
		t.Fatal(err)
	}
	ack := readUntil(t, conn, "door_opened")
	if ack.DeviceID != "COM3" {
		t.Fatalf("ack = %+v", ack)
	}
	if !hw.saw("opened", "COM3") {
		t.Fatal("hardware was not asked to open the door")
	}
}

func TestWebSocketInvalidThenListPorts(t *testing.T) {
	t.Parallel()
	hw := &fakeHardware{ports: []string{"COM3", "/dev/ttyUSB0"}}
	ts, hub := newServer(t, hw)
	defer ts.Close()
	defer hub.Close()
	conn := dial(t, ts)
	defer conn.Close()
	_ = readUntil(t, conn, "hello")

	if err := conn.WriteMessage(gorilla.TextMessage, []byte(`{"action":`)); err != nil {
		t.Fatal(err)
	}
	bad := readUntil(t, conn, "error")
	if bad.Error == "" {
		t.Fatal("expected error text")
	}
	if err := conn.WriteJSON(ws.Inbound{Action: ws.ActionListPorts}); err != nil {
		t.Fatal(err)
	}
	msg := readUntil(t, conn, "ports")
	if strings.Join(msg.Ports, ",") != "COM3,/dev/ttyUSB0" {
		t.Fatalf("ports = %v", msg.Ports)
	}
}

func TestWebSocketHardwareError(t *testing.T) {
	t.Parallel()
	hw := &fakeHardware{doorErr: errors.New("port busy")}
	ts, hub := newServer(t, hw)
	defer ts.Close()
	defer hub.Close()
	conn := dial(t, ts)
	defer conn.Close()
	_ = readUntil(t, conn, "hello")
	if err := conn.WriteJSON(ws.Inbound{Action: ws.ActionOpenDoor, DeviceID: "COM3"}); err != nil {
		t.Fatal(err)
	}
	msg := readUntil(t, conn, "error")
	if !strings.Contains(msg.Error, "port busy") {
		t.Fatalf("error = %+v", msg)
	}
}

func TestWebSocketReplaysRecentHardwareRead(t *testing.T) {
	t.Parallel()
	hw := &fakeHardware{}
	ts, hub := newServer(t, hw)
	defer ts.Close()
	defer hub.Close()
	hub.Broadcast(ws.Outbound{Event: "hardware_read", Data: "12345678", DeviceID: "COM3"})

	conn := dial(t, ts)
	defer conn.Close()
	msg := readUntil(t, conn, "hardware_read")
	if msg.Data != "12345678" {
		t.Fatalf("replay = %+v", msg)
	}
}

func TestHTTPDashboardAndAPI(t *testing.T) {
	t.Parallel()
	hw := &fakeHardware{ports: []string{"COM3"}}
	ts, hub := newServer(t, hw)
	defer ts.Close()
	defer hub.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "HE Gateway") || !strings.Contains(string(body), "/ws") {
		t.Fatalf("dashboard status %d body %q", res.StatusCode, body)
	}

	res, err = http.Get(ts.URL + "/api/ports")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var ports []string
	if err := json.NewDecoder(res.Body).Decode(&ports); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ports, ",") != "COM3" {
		t.Fatalf("ports = %v", ports)
	}

	conn := dial(t, ts)
	defer conn.Close()
	_ = readUntil(t, conn, "hello")
	res, err = http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var status ws.Status
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Clients != 1 {
		t.Fatalf("status = %+v", status)
	}
}

func newServer(t *testing.T, hw ws.Hardware) (*httptest.Server, *ws.Hub) {
	t.Helper()
	hub := ws.NewHub(nil)
	srv := ws.NewServer(hub, hw, web.IndexHTML, nil)
	return httptest.NewServer(srv.Handler()), hub
}

func dial(t *testing.T, ts *httptest.Server) *gorilla.Conn {
	t.Helper()
	conn, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readUntil(t *testing.T, conn *gorilla.Conn, event string) ws.Outbound {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg ws.Outbound
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s: %v", event, err)
		}
		if msg.Event == event {
			return msg
		}
	}
}

type fakeHardware struct {
	mu        sync.Mutex
	ports     []string
	listening []string
	listErr   error
	doorErr   error
	opened    []string
	listened  []string
	stopped   []string
	written   []string
}

func (f *fakeHardware) ListPorts(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.ports...), nil
}

func (f *fakeHardware) Listening() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listening...)
}

func (f *fakeHardware) OpenDoor(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.doorErr != nil {
		return f.doorErr
	}
	f.opened = append(f.opened, deviceID)
	return nil
}

func (f *fakeHardware) StartListen(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listened = append(f.listened, deviceID)
	f.listening = append(f.listening, deviceID)
	return nil
}

func (f *fakeHardware) StopListen(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, deviceID)
	return nil
}

func (f *fakeHardware) Write(ctx context.Context, deviceID string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, deviceID+":"+string(payload))
	return nil
}

func (f *fakeHardware) saw(kind, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []string
	switch kind {
	case "opened":
		items = f.opened
	default:
		return false
	}
	for _, item := range items {
		if item == id {
			return true
		}
	}
	return false
}
