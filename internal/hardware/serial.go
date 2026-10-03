// Package hardware reads and writes local serial devices.
package hardware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// MaxSerialBuffer is the longest identifier accepted from a serial line.
const MaxSerialBuffer = 1024

var (
	// ErrAlreadyListening is returned when a read loop is already running.
	ErrAlreadyListening = errors.New("already listening")
	// ErrNotListening is returned when stop is requested for an idle device.
	ErrNotListening = errors.New("not listening")
	// ErrClosed is returned when the manager has been shut down.
	ErrClosed = errors.New("manager closed")

	// DefaultOpenCommand is the LCUS-1 "relay on" frame.
	DefaultOpenCommand = []byte{0xA0, 0x01, 0x01, 0xA2}
	// DefaultCloseCommand is the LCUS-1 "relay off" frame.
	DefaultCloseCommand = []byte{0xA0, 0x01, 0x00, 0xA1}
)

// SerialDevice is the subset of a serial port used by the gateway.
// Tests inject a mock that implements this interface.
type SerialDevice interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	SetReadTimeout(d time.Duration) error
	Close() error
}

// Mode is the portable serial configuration passed to an Opener.
type Mode struct {
	BaudRate int
	DataBits int
	Parity   string
	StopBits int
}

// Opener lists and opens serial ports. SystemOpener talks to the OS.
type Opener interface {
	List() ([]string, error)
	Open(name string, mode Mode) (SerialDevice, error)
}

// ReadEvent is one identifier read from a device.
type ReadEvent struct {
	DeviceID string
	Data     string
}

// Config tunes the manager. Use DefaultConfig for production defaults.
// A zero Pulse writes the open frame and leaves the relay energized.
type Config struct {
	BaudRate     int
	ReadTimeout  time.Duration
	OpenCommand  []byte
	CloseCommand []byte
	Pulse        time.Duration
	// OnRead is called from the port read goroutine.
	// It must not call StopListen or Close on the same manager.
	OnRead func(ReadEvent)
}

// DefaultConfig returns settings for a 9600 8N1 link and a 300ms door pulse.
func DefaultConfig() Config {
	return Config{
		BaudRate:     9600,
		ReadTimeout:  200 * time.Millisecond,
		OpenCommand:  bytes.Clone(DefaultOpenCommand),
		CloseCommand: bytes.Clone(DefaultCloseCommand),
		Pulse:        300 * time.Millisecond,
	}
}

// SystemOpener opens ports with go.bug.st/serial.
type SystemOpener struct{}

// List returns the OS serial port names.
func (SystemOpener) List() ([]string, error) {
	return serial.GetPortsList()
}

// Open opens one port. The caller owns the returned device.
func (SystemOpener) Open(name string, mode Mode) (SerialDevice, error) {
	return serial.Open(name, ToSerialMode(mode))
}

// Manager owns listen loops and one-shot writes.
type Manager struct {
	opener Opener
	cfg    Config
	logger *slog.Logger

	mu        sync.Mutex
	listeners map[string]*listener
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
}

type listener struct {
	cancel context.CancelFunc
	port   SerialDevice
	done   chan struct{}
	mu     sync.Mutex
}

// NewManager starts a manager bound to opener. Close releases every port.
func NewManager(opener Opener, cfg Config, logger *slog.Logger) *Manager {
	if cfg.BaudRate == 0 {
		cfg.BaudRate = 9600
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 200 * time.Millisecond
	}
	if cfg.OpenCommand == nil {
		cfg.OpenCommand = bytes.Clone(DefaultOpenCommand)
	}
	if cfg.CloseCommand == nil {
		cfg.CloseCommand = bytes.Clone(DefaultCloseCommand)
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		opener:    opener,
		cfg:       cfg,
		logger:    logger,
		listeners: make(map[string]*listener),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// ListPorts returns the names currently exposed by the operating system.
func (m *Manager) ListPorts(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.opener == nil {
		return nil, fmt.Errorf("list serial ports: no opener configured")
	}
	ports, err := m.opener.List()
	if err != nil {
		return nil, fmt.Errorf("list serial ports: %w", err)
	}
	if ports == nil {
		ports = []string{}
	}
	sort.Strings(ports)
	return ports, nil
}

// Listening returns the device ids with an active read loop.
func (m *Manager) Listening() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.listeners))
	for id := range m.listeners {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// StartListen opens deviceID and emits OnRead for every complete line.
func (m *Manager) StartListen(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDeviceID(deviceID); err != nil {
		return fmt.Errorf("start listen %s: %w", deviceID, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("start listen %s: %w", deviceID, ErrClosed)
	}
	if _, ok := m.listeners[deviceID]; ok {
		return fmt.Errorf("start listen %s: %w", deviceID, ErrAlreadyListening)
	}
	port, err := m.open(deviceID)
	if err != nil {
		return err
	}
	lctx, cancel := context.WithCancel(m.ctx)
	l := &listener{
		cancel: cancel,
		port:   port,
		done:   make(chan struct{}),
	}
	m.listeners[deviceID] = l
	m.wg.Add(1)
	go m.readLoop(lctx, deviceID, l)
	return nil
}

// StopListen cancels the read loop and waits until the port is released.
func (m *Manager) StopListen(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	l, ok := m.listeners[deviceID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("stop listen %s: %w", deviceID, ErrNotListening)
	}
	l.cancel()
	closeDevice(l)
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop listen %s: %w", deviceID, ctx.Err())
	}
}

// OpenDoor pulses the relay attached to deviceID.
// A device that is already being read is written in place.
func (m *Manager) OpenDoor(ctx context.Context, deviceID string) error {
	if len(m.cfg.OpenCommand) == 0 {
		return fmt.Errorf("open door %s: open command is empty", deviceID)
	}
	return m.withDevice(ctx, deviceID, func(write func([]byte) error) error {
		if err := m.pulse(ctx, write); err != nil {
			return fmt.Errorf("open door %s: %w", deviceID, err)
		}
		return nil
	})
}

// Write sends payload to deviceID, reusing the listen port when one is open.
func (m *Manager) Write(ctx context.Context, deviceID string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("write %s: payload is empty", deviceID)
	}
	err := m.withDevice(ctx, deviceID, func(write func([]byte) error) error {
		if err := write(payload); err != nil {
			return fmt.Errorf("write serial: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", deviceID, err)
	}
	return nil
}

// Close cancels every listen loop and waits for the ports to close.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	listeners := make([]*listener, 0, len(m.listeners))
	for _, l := range m.listeners {
		listeners = append(listeners, l)
	}
	m.mu.Unlock()
	for _, l := range listeners {
		l.cancel()
		closeDevice(l)
	}
	m.wg.Wait()
	return nil
}

func (m *Manager) withDevice(ctx context.Context, deviceID string, fn func(func([]byte) error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDeviceID(deviceID); err != nil {
		return err
	}
	m.mu.Lock()
	l := m.listeners[deviceID]
	m.mu.Unlock()
	if l != nil {
		return fn(l.write)
	}
	port, err := m.open(deviceID)
	if err != nil {
		return err
	}
	defer port.Close()
	return fn(func(payload []byte) error {
		return writeAll(port, payload)
	})
}

func (l *listener) write(payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.port == nil {
		return fmt.Errorf("serial port is closed")
	}
	return writeAll(l.port, payload)
}

func (m *Manager) open(deviceID string) (SerialDevice, error) {
	if m.opener == nil {
		return nil, fmt.Errorf("open serial port %s: no opener configured", deviceID)
	}
	port, err := m.opener.Open(deviceID, Mode{
		BaudRate: m.cfg.BaudRate,
		DataBits: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", deviceID, err)
	}
	if err := port.SetReadTimeout(m.cfg.ReadTimeout); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("set read timeout on %s: %w", deviceID, err)
	}
	return port, nil
}

func (m *Manager) pulse(ctx context.Context, write func([]byte) error) error {
	if err := write(m.cfg.OpenCommand); err != nil {
		return fmt.Errorf("write open command: %w", err)
	}
	if m.cfg.Pulse <= 0 || len(m.cfg.CloseCommand) == 0 {
		return nil
	}
	timer := time.NewTimer(m.cfg.Pulse)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = write(m.cfg.CloseCommand)
		return fmt.Errorf("pulse door: %w", ctx.Err())
	case <-timer.C:
	}
	if err := write(m.cfg.CloseCommand); err != nil {
		return fmt.Errorf("write close command: %w", err)
	}
	return nil
}

func (m *Manager) readLoop(ctx context.Context, deviceID string, l *listener) {
	defer m.wg.Done()
	defer close(l.done)
	defer m.deleteListener(deviceID, l)
	defer closeDevice(l)

	buf := make([]byte, 256)
	acc := make([]byte, 0, 256)
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := l.read(buf)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if isTimeout(err) {
				continue
			}
			m.logger.Error("serial read", "device_id", deviceID, "err", err)
			return
		}
		if n == 0 {
			continue
		}
		acc = append(acc, buf[:n]...)
		lines, rest := SplitLines(acc)
		for _, line := range lines {
			data, ok := NormalizeLine(line)
			if !ok || m.cfg.OnRead == nil {
				continue
			}
			m.cfg.OnRead(ReadEvent{DeviceID: deviceID, Data: data})
		}
		if len(lines) > 0 {
			acc = append([]byte(nil), rest...)
		}
		if len(acc) > MaxSerialBuffer {
			m.logger.Warn("discarding oversized serial buffer", "device_id", deviceID, "bytes", len(acc))
			acc = nil
		}
	}
}

func (l *listener) read(p []byte) (int, error) {
	l.mu.Lock()
	port := l.port
	l.mu.Unlock()
	if port == nil {
		return 0, io.EOF
	}
	return port.Read(p)
}

func closeDevice(l *listener) {
	l.mu.Lock()
	port := l.port
	l.port = nil
	l.mu.Unlock()
	if port != nil {
		_ = port.Close()
	}
}

func (m *Manager) deleteListener(deviceID string, l *listener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.listeners[deviceID]; ok && cur == l {
		delete(m.listeners, deviceID)
	}
}

func writeAll(port SerialDevice, payload []byte) error {
	for len(payload) > 0 {
		n, err := port.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("short write")
		}
		payload = payload[n:]
	}
	return nil
}

// NormalizeLine trims a serial chunk into an identifier.
// Empty lines and non-printable frames are rejected.
func NormalizeLine(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, "\x00", "")
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxSerialBuffer {
		return "", false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return raw, true
}

// SplitLines splits a serial buffer on CR, LF, and CRLF.
// The remainder is the unfinished tail after the last delimiter.
func SplitLines(buf []byte) (lines []string, rest []byte) {
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] != '\n' && buf[i] != '\r' {
			continue
		}
		lines = append(lines, string(buf[start:i]))
		if buf[i] == '\r' && i+1 < len(buf) && buf[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start == 0 {
		return nil, buf
	}
	return lines, buf[start:]
}

func validateDeviceID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("device_id is required")
	}
	if len(id) > 128 {
		return fmt.Errorf("device_id is too long")
	}
	if strings.ContainsAny(id, "\r\n\x00") {
		return fmt.Errorf("device_id contains invalid characters")
	}
	return nil
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// ToSerialMode maps a portable Mode onto the serial library configuration.
func ToSerialMode(mode Mode) *serial.Mode {
	dataBits := mode.DataBits
	if dataBits == 0 {
		dataBits = 8
	}
	parity := serial.NoParity
	switch strings.ToUpper(strings.TrimSpace(mode.Parity)) {
	case "O", "ODD":
		parity = serial.OddParity
	case "E", "EVEN":
		parity = serial.EvenParity
	case "M", "MARK":
		parity = serial.MarkParity
	case "S", "SPACE":
		parity = serial.SpaceParity
	}
	stop := serial.OneStopBit
	if mode.StopBits == 2 {
		stop = serial.TwoStopBits
	}
	return &serial.Mode{
		BaudRate: mode.BaudRate,
		DataBits: dataBits,
		Parity:   parity,
		StopBits: stop,
	}
}
