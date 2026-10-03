// Package cli parses the gateway process flags.
package cli

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration derived from command-line flags.
type Config struct {
	Addr     string
	Baud     int
	Pulse    time.Duration
	Devices  []string
	JSONLogs bool
	OpenCmd  []byte
	CloseCmd []byte
	CertFile string
	KeyFile  string
}

// ParseArgs reads gateway flags from args. Flag help returns flag.ErrHelp.
func ParseArgs(args []string) (Config, error) {
	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", "127.0.0.1:8080", "HTTP listen address")
	baud := fs.Int("baud", 9600, "serial baud rate")
	devices := fs.String("devices", "", "comma-separated serial ports to listen on at startup")
	pulse := fs.Duration("pulse", 300*time.Millisecond, "door relay pulse width; 0 leaves the relay open")
	openHex := fs.String("open-hex", "", "relay open frame as hex (default A00101A2)")
	closeHex := fs.String("close-hex", "", "relay close frame as hex (default A00100A1)")
	jsonLogs := fs.Bool("log-json", false, "write structured JSON logs")
	certFile := fs.String("cert", "", "TLS certificate for wss (optional)")
	keyFile := fs.String("key", "", "TLS private key for wss (optional)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *baud <= 0 {
		return Config{}, fmt.Errorf("baud must be positive")
	}
	if (*certFile == "") != (*keyFile == "") {
		return Config{}, fmt.Errorf("cert and key must be set together")
	}
	openCmd, err := ParseHex(*openHex)
	if err != nil {
		return Config{}, err
	}
	closeCmd, err := ParseHex(*closeHex)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:     *addr,
		Baud:     *baud,
		Pulse:    *pulse,
		Devices:  splitList(*devices),
		JSONLogs: *jsonLogs,
		OpenCmd:  openCmd,
		CloseCmd: closeCmd,
		CertFile: *certFile,
		KeyFile:  *keyFile,
	}, nil
}

// ParseHex decodes a hex byte string. Spaces and a leading 0x are ignored.
// An empty string returns nil.
func ParseHex(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0x")
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return nil, nil
	}
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("hex command %q has odd length", raw)
	}
	out := make([]byte, len(raw)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(raw[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("hex command %q: %w", raw, err)
		}
		out[i] = byte(v)
	}
	return out, nil
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
