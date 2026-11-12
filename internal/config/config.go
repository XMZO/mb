// Package config 负责读取环境变量与 .env 文件。
package config

import (
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAddr       = ":15000"
	DefaultDBPath     = "./mb.db"
	DefaultCookieName = "mb_admin"

	defaultPort = "15000"
)

// LoginLimit 是登录限流参数;MaxFailures、Window、Lockout 任一为 0 即关闭限流。
type LoginLimit struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
	MaxEntries  int
}

// Config 是进程级配置,启动后不再变化。
type Config struct {
	Addr          string
	DBPath        string
	CookieName    string
	AdminPassword string // 非空时以它为准,每次启动同步到数据库
	Login         LoginLimit

	// TrustProxyHeaders 为 true 时才会读取 X-Forwarded-For / X-Real-IP;
	// TrustedProxies 非空时,只有来自这些网段的请求才被视为可信代理。
	TrustProxyHeaders bool
	TrustedProxies    []netip.Prefix
}

// Load 从环境变量读取配置,缺省或非法的值回退到默认值。
func Load() Config {
	return Config{
		Addr:          normalizeAddr(envString("MB_ADDR", DefaultAddr)),
		DBPath:        envString("MB_DB", DefaultDBPath),
		CookieName:    envString("MB_COOKIE_NAME", DefaultCookieName),
		AdminPassword: envString("MB_ADMIN_PASSWORD", ""),
		Login: LoginLimit{
			MaxFailures: envInt("MB_LOGIN_MAX_FAILURES", 5),
			Window:      envDuration("MB_LOGIN_WINDOW", 15*time.Minute),
			Lockout:     envDuration("MB_LOGIN_LOCKOUT", 15*time.Minute),
			MaxEntries:  envInt("MB_LOGIN_MAX_ENTRIES", 4096),
		},
		TrustProxyHeaders: envBool("MB_TRUST_PROXY_HEADERS", false),
		TrustedProxies:    parsePrefixes(envString("MB_TRUSTED_PROXY_CIDRS", "")),
	}
}

// HealthcheckURL 根据监听地址推导本机健康检查地址。
func HealthcheckURL(addr string) string {
	return "http://127.0.0.1:" + listenPort(addr) + "/healthz"
}

func listenPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return defaultPort
	}
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	if !strings.Contains(addr, ":") {
		return addr // 只写了端口号,例如 MB_ADDR=15001
	}
	return defaultPort
}

// normalizeAddr 允许 MB_ADDR 只写端口号。
func normalizeAddr(addr string) string {
	if _, err := strconv.Atoi(addr); err == nil {
		return ":" + addr
	}
	return addr
}

func envString(key, fallback string) string {
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
		log.Printf("config: invalid %s=%q, using default %d", key, raw, fallback)
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
	}
	log.Printf("config: invalid %s=%q, using default %v", key, raw, fallback)
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < 0 {
		log.Printf("config: invalid %s=%q, using default %s", key, raw, fallback)
		return fallback
	}
	return v
}

// parsePrefixes 解析逗号分隔的 IP / CIDR 列表,非法条目记录日志后忽略。
func parsePrefixes(raw string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if addr, err := netip.ParseAddr(part); err == nil {
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			log.Printf("config: invalid MB_TRUSTED_PROXY_CIDRS entry %q, ignored", part)
			continue
		}
		out = append(out, prefix.Masked())
	}
	return out
}
