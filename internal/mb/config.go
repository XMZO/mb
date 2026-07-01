package mb

import (
	"bufio"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func loadConfig() config {
	return config{
		addr:              envOr("MB_ADDR", defaultAddr),
		dbPath:            envOr("MB_DB", defaultDBPath),
		cookieName:        envOr("MB_COOKIE_NAME", defaultCookieName),
		adminPassword:     envOr("MB_ADMIN_PASSWORD", ""),
		loginMaxFailures:  envInt("MB_LOGIN_MAX_FAILURES", defaultLoginMaxFailures),
		loginWindow:       envDuration("MB_LOGIN_WINDOW", defaultLoginWindow),
		loginLockout:      envDuration("MB_LOGIN_LOCKOUT", defaultLoginLockout),
		loginMaxEntries:   envInt("MB_LOGIN_MAX_ENTRIES", defaultLoginMaxEntries),
		trustProxyHeaders: envBool("MB_TRUST_PROXY_HEADERS", false),
		trustedProxyCIDRs: parseTrustedProxyCIDRs(envOr("MB_TRUSTED_PROXY_CIDRS", "")),
	}
}
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("invalid %s=%q, using default %d", key, raw, fallback)
		return fallback
	}
	return v
}
func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("invalid %s=%q, using default %v", key, raw, fallback)
		return fallback
	}
}
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < 0 {
		log.Printf("invalid %s=%q, using default %s", key, raw, fallback)
		return fallback
	}
	return v
}
func parseTrustedProxyCIDRs(raw string) []*net.IPNet {
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			bits := 128
			if v4 := ip.To4(); v4 != nil {
				ip = v4
				bits = 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, cidr, err := net.ParseCIDR(part)
		if err != nil {
			log.Printf("invalid MB_TRUSTED_PROXY_CIDRS entry %q, ignored", part)
			continue
		}
		out = append(out, cidr)
	}
	return out
}

// loadDotEnv 读取指定 .env 文件(不存在则静默跳过),把其中的 KEY=VALUE 注入进程环境变量。
// 已存在的真实环境变量优先,不会被覆盖(与容器 / systemd 的注入共存)。零依赖,
// 仅支持最常见的 KEY=VALUE、# 注释、可选 export 前缀与成对引号。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, trimQuotes(strings.TrimSpace(line[eq+1:])))
	}
}
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
