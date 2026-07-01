package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"mb/internal/mb"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	mb.Run()
}

func runHealthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthcheckURL(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("healthcheck failed: %s", resp.Status)
	}
	return nil
}

func healthcheckURL() string {
	return "http://127.0.0.1:" + healthcheckPort(os.Getenv("MB_ADDR")) + "/login"
}

func healthcheckPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "15000"
	}
	_, port, err := net.SplitHostPort(addr)
	if err == nil && port != "" {
		return port
	}
	if strings.HasPrefix(addr, ":") {
		port = strings.TrimPrefix(addr, ":")
		if port != "" {
			return port
		}
	}
	if !strings.Contains(addr, ":") {
		return addr
	}
	return "15000"
}
