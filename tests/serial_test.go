package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"go.bug.st/serial"

	hw "he-gateway/internal/hardware"
	"he-gateway/internal/websocket"
)

func TestNormalizeLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "plain", in: "12345678", want: "12345678", ok: true},
		{name: "trim", in: "  AB-12  ", want: "AB-12", ok: true},
		{name: "nulls", in: "12\x0034", want: "1234", ok: true},
		{name: "empty", in: "", ok: false},
		{name: "whitespace", in: " \r ", ok: false},
		{name: "only nulls", in: "\x00\x00", ok: false},
		{name: "control", in: "12\x0134", ok: false},
		{name: "too long", in: strings.Repeat("A", hw.MaxSerialBuffer+1), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := hw.NormalizeLine(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("hw.NormalizeLine(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		in    string
		lines []string
		rest  string
	}{
		{name: "lf", in: "111\n222\n", lines: []string{"111", "222"}, rest: ""},
		{name: "crlf", in: "ABC\r\n", lines: []string{"ABC"}, rest: ""},
		{name: "partial", in: "12", lines: nil, rest: "12"},
		{name: "mixed", in: "A\rB\nC", lines: []string{"A", "B"}, rest: "C"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines, rest := hw.SplitLines([]byte(tt.in))
			if strings.Join(lines, "|") != strings.Join(tt.lines, "|") || string(rest) != tt.rest {
				t.Fatalf("hw.SplitLines(%q) = %q, %q", tt.in, lines, rest)
			}
		})
	}
}

func TestToSerialMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   hw.Mode
		par  serial.Parity
		stop serial.StopBits
		bits int
	}{
		{name: "defaults", in: hw.Mode{BaudRate: 9600}, par: serial.NoParity, stop: serial.OneStopBit, bits: 8},
		{name: "even two", in: hw.Mode{BaudRate: 115200, DataBits: 7, Parity: "even", StopBits: 2}, par: serial.EvenParity, stop: serial.TwoStopBits, bits: 7},
		{name: "odd", in: hw.Mode{Parity: "O"}, par: serial.OddParity, stop: serial.OneStopBit, bits: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hw.ToSerialMode(tt.in)
			if got.BaudRate != tt.in.BaudRate || got.DataBits != tt.bits || got.Parity != tt.par || got.StopBits != tt.stop {
				t.Fatalf("mode = %+v", got)
			}
		})
	}
}

func TestOpenDoorRejectsBadDeviceID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "blank", id: "   "},
		{name: "newline", id: "COM3\n"},
		{name: "too long", id: strings.Repeat("A", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := hw.NewManager(&scriptedOpener{}, testConfig(), nil)
			defer m.Close()
			err := m.OpenDoor(context.Background(), tt.id)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestManagerEmitsHardwareRead(t *testing.T) {
	t.Parallel()
	port := newScriptedPort([]byte("12"), []byte("345678\r\n"))
	opener := newScriptedOpener("COM3", port)
	got := make(chan hw.ReadEvent, 1)
	cfg := testConfig()
	cfg.OnRead = func(ev hw.ReadEvent) { got <- ev }
	m := hw.NewManager(opener, cfg, nil)
	defer m.Close()

	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-got:
		if ev.DeviceID != "COM3" || ev.Data != "12345678" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hardware read")
	}
}

func TestManagerEmitsEachLine(t *testing.T) {
	t.Parallel()
	port := newScriptedPort([]byte("\nAAA\r\n\x01\nBBB\n"))
	opener := newScriptedOpener("COM3", port)
	got := make(chan hw.ReadEvent, 4)
	cfg := testConfig()
	cfg.OnRead = func(ev hw.ReadEvent) { got <- ev }
	m := hw.NewManager(opener, cfg, nil)
	defer m.Close()
	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	var data []string
	deadline := time.After(2 * time.Second)
	for len(data) < 2 {
		select {
		case ev := <-got:
			data = append(data, ev.Data)
		case <-deadline:
			t.Fatalf("events = %v", data)
		}
	}
	if strings.Join(data, ",") != "AAA,BBB" {
		t.Fatalf("events = %v", data)
	}
}

func TestOpenDoorPulse(t *testing.T) {
	t.Parallel()
	port := newScriptedPort()
	opener := newScriptedOpener("COM3", port)
	cfg := testConfig()
	cfg.OpenCommand = []byte("OPEN")
	cfg.CloseCommand = []byte("SHUT")
	cfg.Pulse = 15 * time.Millisecond
	m := hw.NewManager(opener, cfg, nil)
	defer m.Close()

	if err := m.OpenDoor(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(port.Written(), []byte("OPENSHUT")) {
		t.Fatalf("written = %q", port.Written())
	}
	if opener.Opens() != 1 {
		t.Fatalf("opens = %d", opener.Opens())
	}
}

func TestOpenDoorWhileListeningReusesPort(t *testing.T) {
	t.Parallel()
	port := newScriptedPort()
	opener := newScriptedOpener("COM3", port)
	cfg := testConfig()
	cfg.OpenCommand = []byte("OPEN")
	cfg.Pulse = 0
	m := hw.NewManager(opener, cfg, nil)
	defer m.Close()

	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenDoor(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if opener.Opens() != 1 {
		t.Fatalf("opens = %d, want 1", opener.Opens())
	}
	if !bytes.Contains(port.Written(), []byte("OPEN")) {
		t.Fatalf("written = %q", port.Written())
	}
	if err := m.StopListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenDoor(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if opener.Opens() != 2 {
		t.Fatalf("opens = %d, want 2", opener.Opens())
	}
}

func TestWriteShortChunks(t *testing.T) {
	t.Parallel()
	port := newScriptedPort()
	port.chunk = 1
	opener := newScriptedOpener("/dev/ttyUSB0", port)
	m := hw.NewManager(opener, testConfig(), nil)
	defer m.Close()
	if err := m.Write(context.Background(), "/dev/ttyUSB0", []byte("PING")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(port.Written(), []byte("PING")) {
		t.Fatalf("written = %q", port.Written())
	}
}

func TestStartListenGuards(t *testing.T) {
	t.Parallel()
	port := newScriptedPort()
	opener := newScriptedOpener("COM3", port)
	m := hw.NewManager(opener, testConfig(), nil)
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.StartListen(ctx, "COM3"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start: %v", err)
	}

	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	err := m.StartListen(context.Background(), "COM3")
	if !errors.Is(err, hw.ErrAlreadyListening) {
		t.Fatalf("second start: %v", err)
	}
	if err := m.StopListen(context.Background(), "COM4"); !errors.Is(err, hw.ErrNotListening) {
		t.Fatalf("stop missing: %v", err)
	}
	if err := m.StopListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	if listening := m.Listening(); len(listening) != 0 {
		t.Fatalf("listening = %v", listening)
	}
}

func TestListPorts(t *testing.T) {
	t.Parallel()
	opener := newScriptedOpener("COM3", newScriptedPort())
	opener.names = []string{"COM10", "COM3"}
	m := hw.NewManager(opener, testConfig(), nil)
	defer m.Close()
	ports, err := m.ListPorts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ports, ",") != "COM10,COM3" {
		t.Fatalf("ports = %v", ports)
	}

	opener.listErr = errors.New("busy")
	_, err = m.ListPorts(context.Background())
	if err == nil || !errors.Is(err, opener.listErr) {
		t.Fatalf("list error = %v", err)
	}
}

func TestOpenDoorWrapsOpenerError(t *testing.T) {
	t.Parallel()
	opener := newScriptedOpener("COM3", newScriptedPort())
	opener.openErr = errors.New("busy")
	m := hw.NewManager(opener, testConfig(), nil)
	defer m.Close()
	err := m.OpenDoor(context.Background(), "COM3")
	if err == nil || !errors.Is(err, opener.openErr) || !strings.Contains(err.Error(), "COM3") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerCloseUnblocksListener(t *testing.T) {
	t.Parallel()
	port := newScriptedPort()
	m := hw.NewManager(newScriptedOpener("COM3", port), testConfig(), nil)
	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		if err := m.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.StartListen(context.Background(), "COM3"); !errors.Is(err, hw.ErrClosed) {
		t.Fatalf("start after close: %v", err)
	}
}

func TestSerialReadReachesWebsocket(t *testing.T) {
	t.Parallel()
	port := newScriptedPort([]byte("12345678\n"))
	cfg := testConfig()
	hub := websocket.NewHub(nil)
	defer hub.Close()
	cfg.OnRead = func(ev hw.ReadEvent) {
		hub.Broadcast(websocket.Outbound{
			Event:    "hardware_read",
			DeviceID: ev.DeviceID,
			Data:     ev.Data,
		})
	}
	m := hw.NewManager(newScriptedOpener("COM3", port), cfg, nil)
	defer m.Close()

	srv := websocket.NewServer(hub, m, []byte("<!doctype html><title>HE Gateway</title>"), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := m.StartListen(context.Background(), "COM3"); err != nil {
		t.Fatal(err)
	}
	msg := readWSUntil(t, conn, "hardware_read")
	if msg.Data != "12345678" || msg.DeviceID != "COM3" {
		t.Fatalf("message = %+v", msg)
	}
}

func testConfig() hw.Config {
	cfg := hw.DefaultConfig()
	cfg.ReadTimeout = 20 * time.Millisecond
	cfg.Pulse = 0
	cfg.OpenCommand = []byte("OPEN")
	cfg.CloseCommand = []byte("SHUT")
	return cfg
}

func readWSUntil(t *testing.T, conn *gorilla.Conn, event string) websocket.Outbound {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg websocket.Outbound
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s: %v", event, err)
		}
		if msg.Event == event {
			return msg
		}
	}
}

// scriptedPort is a hw.SerialDevice mock. Tests never open a real COM port.
type scriptedPort struct {
	mu      sync.Mutex
	reads   [][]byte
	written []byte
	chunk   int
	timeout time.Duration
	closed  bool
	done    chan struct{}
	wake    chan struct{}
}

func newScriptedPort(chunks ...[]byte) *scriptedPort {
	p := &scriptedPort{
		done: make(chan struct{}),
		wake: make(chan struct{}, 1),
	}
	for _, chunk := range chunks {
		p.reads = append(p.reads, append([]byte(nil), chunk...))
	}
	return p
}

func (p *scriptedPort) Read(b []byte) (int, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return 0, io.EOF
		}
		if len(p.reads) > 0 {
			chunk := p.reads[0]
			n := copy(b, chunk)
			if n < len(chunk) {
				p.reads[0] = append([]byte(nil), chunk[n:]...)
			} else {
				p.reads = p.reads[1:]
			}
			p.mu.Unlock()
			return n, nil
		}
		timeout := p.timeout
		p.mu.Unlock()
		if timeout <= 0 {
			timeout = 15 * time.Millisecond
		}
		timer := time.NewTimer(timeout)
		select {
		case <-p.done:
			timer.Stop()
			return 0, io.EOF
		case <-p.wake:
			timer.Stop()
		case <-timer.C:
			return 0, nil
		}
	}
}

func (p *scriptedPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	n := len(b)
	if p.chunk > 0 && p.chunk < n {
		n = p.chunk
	}
	p.written = append(p.written, b[:n]...)
	return n, nil
}

func (p *scriptedPort) SetReadTimeout(d time.Duration) error {
	p.mu.Lock()
	p.timeout = d
	p.mu.Unlock()
	return nil
}

func (p *scriptedPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	close(p.done)
	return nil
}

func (p *scriptedPort) reopen() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		return
	}
	p.closed = false
	p.done = make(chan struct{})
}

func (p *scriptedPort) Written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.written...)
}

type scriptedOpener struct {
	mu      sync.Mutex
	ports   map[string]*scriptedPort
	names   []string
	listErr error
	openErr error
	opens   int
	last    hw.Mode
}

func newScriptedOpener(name string, port *scriptedPort) *scriptedOpener {
	return &scriptedOpener{
		ports: map[string]*scriptedPort{name: port},
		names: []string{name},
	}
}

func (o *scriptedOpener) List() ([]string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.listErr != nil {
		return nil, o.listErr
	}
	return append([]string(nil), o.names...), nil
}

func (o *scriptedOpener) Open(name string, mode hw.Mode) (hw.SerialDevice, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens++
	o.last = mode
	if o.openErr != nil {
		return nil, o.openErr
	}
	port, ok := o.ports[name]
	if !ok {
		return nil, fmt.Errorf("unknown port %s", name)
	}
	port.reopen()
	return port, nil
}

func (o *scriptedOpener) Opens() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opens
}
