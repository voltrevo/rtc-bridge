package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"barney.ci/go-json5"
)

// Config is the validated configuration for webrtc-forward.
type Config struct {
	Target    string
	Signaling SignalingConfig
}

// SignalingConfig describes how SDP offer/answer exchange happens.
type SignalingConfig struct {
	Type string // "stdin" or "http"
	Addr string // required when Type == "http"
}

var allowedTopLevel = []string{"target", "signaling"}
var allowedSignaling = []string{"type", "addr"}

// LoadConfig reads and strictly validates a JSON5 config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
	}

	// Translate JSON5 → JSON, then parse to a raw map for strict key validation.
	jsonBytes, err := io.ReadAll(json5.NewReader(bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}

	// Reject unknown top-level keys.
	if err := rejectUnknown("config", raw, allowedTopLevel); err != nil {
		return nil, err
	}

	cfg := &Config{}

	// ── target ────────────────────────────────────────────────────────────────
	targetVal, ok := raw["target"]
	if !ok {
		return nil, fmt.Errorf(`config: required field "target" is missing`)
	}
	target, ok := targetVal.(string)
	if !ok {
		return nil, fmt.Errorf(`config: "target" must be a string, got %s`, typeName(targetVal))
	}
	if target == "" {
		return nil, fmt.Errorf(`config: "target" must not be empty`)
	}
	cfg.Target = target

	// ── signaling ─────────────────────────────────────────────────────────────
	sigVal, ok := raw["signaling"]
	if !ok {
		return nil, fmt.Errorf(`config: required field "signaling" is missing`)
	}
	sigMap, ok := sigVal.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(`config: "signaling" must be an object, got %s`, typeName(sigVal))
	}
	if err := rejectUnknown("signaling", sigMap, allowedSignaling); err != nil {
		return nil, err
	}

	// signaling.type
	stVal, ok := sigMap["type"]
	if !ok {
		return nil, fmt.Errorf(`config: required field "signaling.type" is missing`)
	}
	sigType, ok := stVal.(string)
	if !ok {
		return nil, fmt.Errorf(`config: "signaling.type" must be a string, got %s`, typeName(stVal))
	}
	if sigType != "stdin" && sigType != "http" {
		return nil, fmt.Errorf(`config: "signaling.type" must be "stdin" or "http", got %q`, sigType)
	}
	cfg.Signaling.Type = sigType

	// signaling.addr
	addrVal, hasAddr := sigMap["addr"]
	if sigType == "http" {
		if !hasAddr {
			return nil, fmt.Errorf(`config: "signaling.addr" is required when signaling.type is "http"`)
		}
		addr, ok := addrVal.(string)
		if !ok {
			return nil, fmt.Errorf(`config: "signaling.addr" must be a string, got %s`, typeName(addrVal))
		}
		if addr == "" {
			return nil, fmt.Errorf(`config: "signaling.addr" must not be empty`)
		}
		cfg.Signaling.Addr = addr
	} else if hasAddr {
		return nil, fmt.Errorf(`config: "signaling.addr" is not allowed when signaling.type is %q`, sigType)
	}

	return cfg, nil
}

// rejectUnknown returns an error if raw contains any key not in allowed.
func rejectUnknown(section string, raw map[string]interface{}, allowed []string) error {
	set := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		set[k] = true
	}
	var unknown []string
	for k := range raw {
		if !set[k] {
			unknown = append(unknown, fmt.Sprintf("%q", k))
		}
	}
	if len(unknown) > 0 {
		quoted := make([]string, len(allowed))
		for i, k := range allowed {
			quoted[i] = fmt.Sprintf("%q", k)
		}
		return fmt.Errorf(`config: unknown field(s) in %s: %s (allowed: %s)`,
			section, strings.Join(unknown, ", "), strings.Join(quoted, ", "))
	}
	return nil
}

// typeName returns a human-readable name for the dynamic type of v.
func typeName(v interface{}) string {
	switch v.(type) {
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// SampleConfig is the content written by `webrtc-forward init`.
const SampleConfig = `{
  // TCP address to forward WebRTC data channel traffic to.
  target: "127.0.0.1:7777",

  signaling: {
    // "stdin" — interactive copy-paste in the terminal (no extra ports needed).
    // "http"  — serves POST /offer for automated / CLI-client use.
    type: "stdin",

    // Uncomment and set addr when type is "http":
    // addr: "127.0.0.1:8765",
  },
}
`
