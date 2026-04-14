package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"barney.ci/go-json5"
)

type config struct {
	Addr string
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config %q: %w", path, err)
	}
	jsonBytes, err := io.ReadAll(json5.NewReader(strings.NewReader(string(data))))
	if err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}
	for k := range raw {
		if k != "addr" {
			return nil, fmt.Errorf("config: unknown field %q (allowed: \"addr\")", k)
		}
	}
	addrVal, ok := raw["addr"]
	if !ok {
		return nil, fmt.Errorf(`config: required field "addr" is missing`)
	}
	addr, ok := addrVal.(string)
	if !ok || addr == "" {
		return nil, fmt.Errorf(`config: "addr" must be a non-empty string`)
	}
	return &config{Addr: addr}, nil
}
