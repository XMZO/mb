package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"mb/internal/config"
)

// loginLimiter 按客户端 IP 限制登录失败次数:
// 在 Window 时间内失败达到 MaxFailures 次后,锁定 Lockout 时长。状态只存在内存里。
type loginLimiter struct {
	limit config.LoginLimit

	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

type loginAttempt struct {
	failures     int
	firstFailure time.Time
	lockedUntil  time.Time
	lastSeen     time.Time
}

func newLoginLimiter(limit config.LoginLimit) *loginLimiter {
	if limit.MaxEntries <= 0 {
		limit.MaxEntries = 4096
	}
	return &loginLimiter{limit: limit, attempts: make(map[string]*loginAttempt)}
}

func (l *loginLimiter) enabled() bool {
	return l.limit.MaxFailures > 0 && l.limit.Window > 0 && l.limit.Lockout > 0
}

// lockedFor 返回 key 剩余的锁定时长;未被锁定时返回 0。
func (l *loginLimiter) lockedFor(key string, now time.Time) time.Duration {
	if !l.enabled() {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[key]
	if !ok {
		return 0
	}
	a.lastSeen = now
	if a.lockedUntil.After(now) {
		return a.lockedUntil.Sub(now)
	}
	return 0
}

// fail 记录一次登录失败;如果因此触发锁定,返回锁定时长,否则返回 0。
func (l *loginLimiter) fail(key string, now time.Time) time.Duration {
	if !l.enabled() {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)

	a, ok := l.attempts[key]
	if !ok || now.Sub(a.firstFailure) > l.limit.Window {
		a = &loginAttempt{firstFailure: now}
		l.attempts[key] = a
	}
	a.failures++
	a.lastSeen = now
	if a.failures >= l.limit.MaxFailures {
		a.lockedUntil = now.Add(l.limit.Lockout)
		return l.limit.Lockout
	}
	return 0
}

// reset 在登录成功后清除 key 的失败记录。
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// prune 清除已过期的记录;条目数仍超过上限时,淘汰最久没有出现的客户端。
func (l *loginLimiter) prune(now time.Time) {
	for key, a := range l.attempts {
		if now.Sub(a.firstFailure) > l.limit.Window && !a.lockedUntil.After(now) {
			delete(l.attempts, key)
		}
	}
	for len(l.attempts) >= l.limit.MaxEntries {
		oldestKey, oldest := "", now
		for key, a := range l.attempts {
			if oldestKey == "" || a.lastSeen.Before(oldest) {
				oldestKey, oldest = key, a.lastSeen
			}
		}
		delete(l.attempts, oldestKey)
	}
}

// clientIP 返回用于限流的客户端地址。
//
// 默认只看 TCP 对端地址。开启 MB_TRUST_PROXY_HEADERS 且对端是可信代理时,才读取代理写入的请求头:
// X-Forwarded-For 从右向左取第一个"不是可信代理"的地址——最右边的条目由离我们最近的代理追加,
// 无法被客户端伪造,而最左边的条目完全由客户端控制,不能用来限流。
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	remote = remote.Unmap()
	if !s.cfg.TrustProxyHeaders || !s.isTrustedProxy(remote) {
		return remote.String()
	}

	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		// 没有配置可信网段时无从分辨多级代理,只能取最近一级代理看到的地址。
		if len(s.cfg.TrustedProxies) == 0 || !s.isTrustedProxy(addr) {
			return addr.String()
		}
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return addr.Unmap().String()
	}
	return remote.String()
}

// isTrustedProxy 判断 addr 是否属于可信代理;没有配置网段时,信任所有直连的对端。
func (s *Server) isTrustedProxy(addr netip.Addr) bool {
	if len(s.cfg.TrustedProxies) == 0 {
		return true
	}
	for _, prefix := range s.cfg.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
