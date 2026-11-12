package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	cases := map[string]string{
		"":                  "15000",
		":15000":            "15000",
		"0.0.0.0:8080":      "8080",
		"127.0.0.1:9000":    "9000",
		"[::]:7000":         "7000",
		"localhost:16000":   "16000",
		"15001":             "15001",
		"invalid:address:x": "15000",
	}
	for addr, port := range cases {
		want := "http://127.0.0.1:" + port + "/healthz"
		if got := HealthcheckURL(addr); got != want {
			t.Errorf("HealthcheckURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestNormalizeAddr(t *testing.T) {
	if got := normalizeAddr("15001"); got != ":15001" {
		t.Errorf("bare port should become a listen address, got %q", got)
	}
	if got := normalizeAddr("127.0.0.1:80"); got != "127.0.0.1:80" {
		t.Errorf("full address should be kept, got %q", got)
	}
}

func TestParseEnvLine(t *testing.T) {
	cases := []struct {
		line, key, value string
		ok               bool
	}{
		{"MB_ADDR=:15000", "MB_ADDR", ":15000", true},
		{"  export MB_DB = ./data/mb.db ", "MB_DB", "./data/mb.db", true},
		{`MB_ADMIN_PASSWORD="with # hash"`, "MB_ADMIN_PASSWORD", "with # hash", true},
		{"MB_COOKIE_NAME=mb # 注释", "MB_COOKIE_NAME", "mb", true},
		{"EMPTY=", "EMPTY", "", true},
		{"# comment", "", "", false},
		{"no equals sign", "", "", false},
		{"=value", "", "", false},
	}
	for _, c := range cases {
		key, value, ok := parseEnvLine(c.line)
		if key != c.key || value != c.value || ok != c.ok {
			t.Errorf("parseEnvLine(%q) = %q, %q, %v", c.line, key, value, ok)
		}
	}
}

func TestLoadDotEnvKeepsRealEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("MB_TEST_FROM_FILE=file\nMB_TEST_FROM_ENV=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MB_TEST_FROM_ENV", "real")
	t.Setenv("MB_TEST_FROM_FILE", "")
	os.Unsetenv("MB_TEST_FROM_FILE")

	LoadDotEnv(path)
	if got := os.Getenv("MB_TEST_FROM_FILE"); got != "file" {
		t.Errorf("value from file = %q", got)
	}
	if got := os.Getenv("MB_TEST_FROM_ENV"); got != "real" {
		t.Errorf("real environment must win, got %q", got)
	}
	LoadDotEnv(filepath.Join(t.TempDir(), "missing")) // 不存在的文件应当静默跳过
}

func TestParsePrefixes(t *testing.T) {
	got := parsePrefixes("127.0.0.1, 10.0.0.0/8, ::1, bogus,")
	want := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	if len(got) != len(want) {
		t.Fatalf("parsePrefixes = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("prefix %d = %v, want %v", i, got[i], want[i])
		}
	}
}
