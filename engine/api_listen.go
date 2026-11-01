package main

import (
	"errors"
	"net"
	"os"
)

// This controls only the public API's first bind, not private harness bridges.
// The unset default retains existing Windows/LAN behavior. VPS installations
// explicitly choose loopback before starting the engine for the first time.
func publicAPIListenAddress() (string, error) {
	host := os.Getenv("KLM_API_BIND_HOST")
	switch host {
	case "":
		host = "0.0.0.0"
	case "127.0.0.1", "::1":
	default:
		return "", errors.New("KLM_API_BIND_HOST must be unset, 127.0.0.1, or ::1")
	}
	return net.JoinHostPort(host, apiPort), nil
}
