package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	ActionOpenDoor   = "open_door"
	ActionListen     = "listen"
	ActionStopListen = "stop_listen"
	ActionListPorts  = "list_ports"
	ActionWrite      = "write"

	maxPayload = 1024
	maxDevice  = 128
)

// Inbound is a command sent by a browser client.
// Example: {"action":"open_door","device_id":"COM3"}.
type Inbound struct {
	Action   string `json:"action"`
	DeviceID string `json:"device_id,omitempty"`
	Payload  string `json:"payload,omitempty"`
}

// Outbound is an event pushed to browser clients.
// A reader emits {"event":"hardware_read","data":"12345678","device_id":"COM3"}.
type Outbound struct {
	Event    string   `json:"event,omitempty"`
	Action   string   `json:"action,omitempty"`
	DeviceID string   `json:"device_id,omitempty"`
	Data     string   `json:"data,omitempty"`
	Error    string   `json:"error,omitempty"`
	Ports    []string `json:"ports,omitzero"`
}

// ParseInbound decodes and validates one JSON command.
func ParseInbound(data []byte) (Inbound, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Inbound{}, fmt.Errorf("parse message: empty payload")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var msg Inbound
	if err := dec.Decode(&msg); err != nil {
		return Inbound{}, fmt.Errorf("parse message: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Inbound{}, fmt.Errorf("parse message: extra data")
		}
		return Inbound{}, fmt.Errorf("parse message: %w", err)
	}
	msg.Action = strings.TrimSpace(msg.Action)
	msg.DeviceID = strings.TrimSpace(msg.DeviceID)
	if err := msg.validate(); err != nil {
		return Inbound{}, err
	}
	return msg, nil
}

func (m Inbound) validate() error {
	switch m.Action {
	case ActionListPorts:
		return nil
	case ActionOpenDoor, ActionListen, ActionStopListen, ActionWrite:
		if err := validateDeviceID(m.DeviceID); err != nil {
			return fmt.Errorf("validate message: %w", err)
		}
		if m.Action == ActionWrite {
			if m.Payload == "" {
				return fmt.Errorf("validate message: payload is required")
			}
			if len(m.Payload) > maxPayload {
				return fmt.Errorf("validate message: payload is too long")
			}
		}
		return nil
	case "":
		return fmt.Errorf("validate message: action is required")
	default:
		return fmt.Errorf("validate message: unknown action %q", m.Action)
	}
}

func validateDeviceID(id string) error {
	if id == "" {
		return fmt.Errorf("device_id is required")
	}
	if len(id) > maxDevice {
		return fmt.Errorf("device_id is too long")
	}
	if strings.ContainsAny(id, "\r\n\x00") {
		return fmt.Errorf("device_id contains invalid characters")
	}
	return nil
}
