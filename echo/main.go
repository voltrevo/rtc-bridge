// echo: a TCP server that echoes back every line it receives, uppercased,
// with a prefix showing the byte count. Great for testing webrtc-forward.
//
// Usage: echo [addr]   (default: 127.0.0.1:7777)
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	addr := "127.0.0.1:7777"
	if len(os.Args) > 1 {
		addr = os.Args[1]
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
