// echo: a TCP server that echoes back every line it receives, uppercased,
// with a prefix showing the byte count. Great for testing webrtc-forward.
//
// Commands:
//
//	echo run  [--config path]   run the server (default config: config.json5)
//	echo init [--config path]   write a sample config file and exit
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"barney.ci/go-json5"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  echo run  [--config path]   run the echo server\n")
	fmt.Fprintf(os.Stderr, "  echo init [--config path]   generate a sample config file\n")
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "config.json5", "path to JSON5 config file")
	fs.Parse(args)

	addr, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen %s: %v\n", addr, err)
		os.Exit(1)
	}
	fmt.Printf("echo server listening on %s\n", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept: %v\n", err)
			continue
		}
		go handle(conn)
	}
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "config.json5", "path to write sample config")
	fs.Parse(args)

	if _, err := os.Stat(*configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %q already exists — delete it first or choose a different path\n", *configPath)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, []byte(sampleConfig), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %q: %v\n", *configPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote sample config to %q\n", *configPath)
}

// loadConfig reads and validates the JSON5 config, returning the listen addr.
func loadConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read config file %q: %w", path, err)
	}

	jsonBytes, err := io.ReadAll(json5.NewReader(bytes.NewReader(data)))
	if err != nil {
		return "", fmt.Errorf("config parse error: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return "", fmt.Errorf("config parse error: %w", err)
	}

	for k := range raw {
		if k != "addr" {
			return "", fmt.Errorf(`config: unknown field %q (allowed: "addr")`, k)
		}
	}

	addrVal, ok := raw["addr"]
	if !ok {
		return "", fmt.Errorf(`config: required field "addr" is missing`)
	}
	addr, ok := addrVal.(string)
	if !ok {
		return "", fmt.Errorf(`config: "addr" must be a string`)
	}
	if addr == "" {
		return "", fmt.Errorf(`config: "addr" must not be empty`)
	}
	return addr, nil
}

func handle(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr()
	fmt.Printf("[%s] connected\n", remote)

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[%s] recv %d bytes: %q\n", remote, len(line), line)
		upper := strings.ToUpper(line)
		ts := time.Now().Format("15:04:05")
		reply := fmt.Sprintf("[%s] (%d bytes) %s\r\n", ts, len(line), upper)
		if _, err := fmt.Fprint(conn, reply); err != nil {
			fmt.Printf("[%s] write error: %v\n", remote, err)
			return
		}
	}
	fmt.Printf("[%s] disconnected\n", remote)
}

const sampleConfig = `{
  // TCP address to listen on.
  addr: "127.0.0.1:7777",
}
`
