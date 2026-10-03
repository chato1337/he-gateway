package tests

import (
	"bytes"
	"errors"
	"flag"
	"testing"

	"he-gateway/internal/cli"
)

func TestParseHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    []byte
		wantErr bool
	}{
		{name: "empty", in: "  ", want: nil},
		{name: "spaced", in: "A0 01 01 A2", want: []byte{0xA0, 0x01, 0x01, 0xA2}},
		{name: "prefix", in: "0xA001", want: []byte{0xA0, 0x01}},
		{name: "odd", in: "A0 1", wantErr: true},
		{name: "bad digit", in: "GG", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := cli.ParseHex(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("got %x, want %x", got, tt.want)
			}
		})
	}
}

func TestParseArgsRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{name: "extra", args: []string{"nope"}},
		{name: "baud", args: []string{"-baud", "0"}},
		{name: "cert only", args: []string{"-cert", "a.pem"}},
		{name: "odd hex", args: []string{"-open-hex", "A0 1"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := cli.ParseArgs(tt.args)
			if err == nil || errors.Is(err, flag.ErrHelp) {
				t.Fatal("expected a parse error")
			}
		})
	}
}

func TestParseArgsDevices(t *testing.T) {
	t.Parallel()
	cfg, err := cli.ParseArgs([]string{"-devices", " COM3, /dev/ttyUSB0 ,"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Devices) != 2 || cfg.Devices[0] != "COM3" || cfg.Devices[1] != "/dev/ttyUSB0" {
		t.Fatalf("devices = %#v", cfg.Devices)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.Baud != 9600 {
		t.Fatalf("cfg = %+v", cfg)
	}
}
