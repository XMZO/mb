package main

import "testing"

func TestHealthcheckPort(t *testing.T) {
	tests := map[string]string{
		"":                  "15000",
		":15000":            "15000",
		"0.0.0.0:8080":      "8080",
		"127.0.0.1:9000":    "9000",
		"[::]:7000":         "7000",
		"localhost:16000":   "16000",
		"15001":             "15001",
		"invalid:address:x": "15000",
	}
	for input, want := range tests {
		if got := healthcheckPort(input); got != want {
			t.Fatalf("healthcheckPort(%q): want %q, got %q", input, want, got)
		}
	}
}
