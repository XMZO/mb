package mb

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ensureAdminPassword 播种 / 同步管理员密码:
//   - 设置了 MB_ADMIN_PASSWORD:以环境变量为准,启动时确保库内密码与之一致
//     (改环境变量重启即生效,适合容器 / IaC 声明式部署)。
//   - 未设置且库内尚无密码:生成随机强密码并打印到日志一次,登录后可在「设置」中修改。
//   - 未设置且库内已有密码:保持不变(交由「设置」页管理)。
func ensureAdminPassword(db *sql.DB, envPassword string) error {
	hash, ok, err := getSetting(db, settingPasswordHash)
	if err != nil {
		return err
	}

	if envPassword != "" {
		if ok && bcrypt.CompareHashAndPassword([]byte(hash), []byte(envPassword)) == nil {
			return nil // 已与环境变量一致,无需改动
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(envPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err := setSetting(db, settingPasswordHash, string(newHash)); err != nil {
			return err
		}
		if ok {
			log.Printf("admin password synced from MB_ADMIN_PASSWORD")
		} else {
			log.Printf("admin password initialized from MB_ADMIN_PASSWORD")
		}
		return nil
	}

	if ok {
		return nil // 已有密码,交由「设置」页管理
	}

	pw, err := randomPassword()
	if err != nil {
		return err
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := setSetting(db, settingPasswordHash, string(newHash)); err != nil {
		return err
	}
	log.Printf("============================================================")
	log.Printf(" 未设置 MB_ADMIN_PASSWORD,已生成随机管理员密码(仅显示这一次):")
	log.Printf("     %s", pw)
	log.Printf(" 请用它登录并尽快在「设置」中修改;或在 .env 里设置 MB_ADMIN_PASSWORD 固定密码。")
	log.Printf("============================================================")
	return nil
}
func randomPassword() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func newCookieSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type requestContextKey string

const (
	cspNonceContextKey  requestContextKey = "csp_nonce"
	csrfTokenContextKey requestContextKey = "csrf_token"
)

type secureResponseWriter struct {
	http.ResponseWriter
	meta RenderMeta
}

func (w *secureResponseWriter) renderMeta() RenderMeta {
	return w.meta
}

func (a *app) withCommon(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce, err := randomToken(18)
		if err != nil {
			a.serverError(w, err)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", a.contentSecurityPolicy(nonce))

		csrfToken, err := a.prepareCSRF(w, r)
		if err != nil {
			log.Printf("csrf rejected: %v", err)
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), cspNonceContextKey, nonce)
		ctx = context.WithValue(ctx, csrfTokenContextKey, csrfToken)
		secureW := &secureResponseWriter{ResponseWriter: w, meta: RenderMeta{CSPNonce: nonce, CSRFToken: csrfToken}}
		next.ServeHTTP(secureW, r.WithContext(ctx))
	})
}

func (a *app) contentSecurityPolicy(nonce string) string {
	q := "'nonce-" + nonce + "'"
	return strings.Join([]string{
		"default-src 'self'",
		"style-src 'self' https: " + q,
		"script-src 'self' https: " + q,
		"img-src 'self' data: https:",
		"connect-src 'self'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
	}, "; ")
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (a *app) prepareCSRF(w http.ResponseWriter, r *http.Request) (string, error) {
	secret, ok, err := getSetting(a.db, settingCookieSecret)
	if err != nil {
		return "", err
	}
	if !ok || secret == "" {
		return "", errors.New("missing cookie secret")
	}

	if isUnsafeMethod(r.Method) {
		return a.validateCSRFRequest(w, r, secret)
	}
	return a.ensureCSRFCookie(w, r, secret)
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func csrfCookieName(cookieName string) string {
	return cookieName + "_csrf"
}

func (a *app) ensureCSRFCookie(w http.ResponseWriter, r *http.Request, secret string) (string, error) {
	if c, err := r.Cookie(csrfCookieName(a.cfg.cookieName)); err == nil {
		if token, ok := validateSignedToken(c.Value, secret); ok {
			return token, nil
		}
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName(a.cfg.cookieName),
		Value:    signToken(token, secret),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(csrfDuration),
		Secure:   requestIsSecure(r),
	})
	return token, nil
}

func (a *app) validateCSRFRequest(w http.ResponseWriter, r *http.Request, secret string) (string, error) {
	cookie, err := r.Cookie(csrfCookieName(a.cfg.cookieName))
	if err != nil {
		return "", errors.New("missing csrf cookie")
	}
	token, ok := validateSignedToken(cookie.Value, secret)
	if !ok {
		return "", errors.New("invalid csrf cookie")
	}
	bodyLimit := maxFormBodyBytes
	if strings.HasPrefix(r.URL.Path, "/admin/backup/import") {
		bodyLimit = backupMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	provided := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if provided == "" {
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if strings.HasPrefix(ct, "multipart/form-data") {
			if err := r.ParseMultipartForm(bodyLimit); err != nil {
				return "", err
			}
		} else if err := r.ParseForm(); err != nil {
			return "", err
		}
		provided = strings.TrimSpace(r.Form.Get(csrfFieldName))
	}
	if provided == "" || !hmac.Equal([]byte(provided), []byte(token)) {
		return "", errors.New("csrf token mismatch")
	}
	return token, nil
}

func signToken(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	sig := mac.Sum(nil)
	return token + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func validateSignedToken(value, secret string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", false
	}
	return parts[0], true
}
func (a *app) withView(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
		if publicMode || a.isAdmin(r) {
			fn(w, r)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
func (a *app) withAdmin(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			if strings.EqualFold(r.Header.Get("X-Requested-With"), "XMLHttpRequest") ||
				strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		fn(w, r)
	}
}
func (a *app) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(a.cfg.cookieName)
	if err != nil {
		return false
	}
	secret, ok, err := getSetting(a.db, settingCookieSecret)
	if err != nil || !ok || secret == "" {
		return false
	}
	return validateSessionCookie(c.Value, secret)
}
func validateSessionCookie(value, secret string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	want := mac.Sum(nil)
	if !hmac.Equal(want, sig) {
		return false
	}

	fields := strings.Split(string(payload), ":")
	if len(fields) != 2 || fields[1] != "admin" {
		return false
	}
	expUnix, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() <= expUnix
}
func makeSessionCookie(secret string) (string, error) {
	exp := time.Now().Add(sessionDuration).Unix()
	payload := []byte(fmt.Sprintf("%d:admin", exp))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func (a *app) setSessionCookie(w http.ResponseWriter, r *http.Request) error {
	secret, ok, err := getSetting(a.db, settingCookieSecret)
	if err != nil || !ok || secret == "" {
		return errors.New("missing cookie secret")
	}
	val, err := makeSessionCookie(secret)
	if err != nil {
		return err
	}
	c := &http.Cookie{
		Name:     a.cfg.cookieName,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionDuration),
		Secure:   requestIsSecure(r),
	}
	http.SetCookie(w, c)
	return nil
}
func (a *app) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cfg.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
func newLoginLimiter(maxFailures int, window, lockout time.Duration, maxEntries int) *loginLimiter {
	if maxEntries <= 0 {
		maxEntries = defaultLoginMaxEntries
	}
	return &loginLimiter{
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
		maxEntries:  maxEntries,
		attempts:    make(map[string]*loginAttempt),
	}
}
func (l *loginLimiter) enabled() bool {
	return l != nil && l.maxFailures > 0 && l.window > 0 && l.lockout > 0
}
func (l *loginLimiter) isLocked(key string, now time.Time) (bool, time.Duration) {
	if !l.enabled() || key == "" {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)

	a, ok := l.attempts[key]
	if !ok {
		return false, 0
	}
	a.LastSeen = now
	if a.LockedUntil.After(now) {
		return true, a.LockedUntil.Sub(now)
	}
	if now.Sub(a.FirstFailure) > l.window {
		delete(l.attempts, key)
	}
	return false, 0
}
func (l *loginLimiter) recordFailure(key string, now time.Time) (bool, time.Duration) {
	if !l.enabled() || key == "" {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)

	a, ok := l.attempts[key]
	if !ok || now.Sub(a.FirstFailure) > l.window {
		a = &loginAttempt{FirstFailure: now}
		l.attempts[key] = a
	}
	a.Failures++
	a.LastSeen = now
	if a.Failures >= l.maxFailures {
		a.LockedUntil = now.Add(l.lockout)
		return true, l.lockout
	}
	return false, 0
}
func (l *loginLimiter) clear(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}
func (l *loginLimiter) cleanupLocked(now time.Time) {
	for key, a := range l.attempts {
		windowExpired := now.Sub(a.FirstFailure) > l.window
		lockExpired := a.LockedUntil.IsZero() || !a.LockedUntil.After(now)
		if windowExpired && lockExpired {
			delete(l.attempts, key)
		}
	}
	for len(l.attempts) > l.maxEntries {
		oldestKey := ""
		var oldest time.Time
		for key, a := range l.attempts {
			if oldestKey == "" || a.LastSeen.Before(oldest) {
				oldestKey = key
				oldest = a.LastSeen
			}
		}
		if oldestKey == "" {
			break
		}
		delete(l.attempts, oldestKey)
	}
}
func (a *app) clientIP(r *http.Request) string {
	remoteHost := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		remoteHost = host
	}
	if a != nil && a.trustProxyHeaders(remoteHost) {
		if ip := firstHeaderIP(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if ip := firstHeaderIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	return remoteHost
}
func (a *app) trustProxyHeaders(remoteHost string) bool {
	if a == nil || !a.cfg.trustProxyHeaders {
		return false
	}
	if len(a.cfg.trustedProxyCIDRs) == 0 {
		return true
	}
	ip := net.ParseIP(remoteHost)
	if ip == nil {
		return false
	}
	for _, cidr := range a.cfg.trustedProxyCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}
func firstHeaderIP(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			return ip.String()
		}
	}
	return ""
}
func retryAfterSeconds(d time.Duration) string {
	if d < time.Second {
		return "1"
	}
	return strconv.Itoa(int(math.Ceil(d.Seconds())))
}
func loginLockedMessage(d time.Duration) string {
	if d < time.Minute {
		return "登录尝试过多,请稍后再试。"
	}
	mins := int(math.Ceil(d.Minutes()))
	return fmt.Sprintf("登录尝试过多,请约 %d 分钟后再试。", mins)
}
func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()

	if a.isAdmin(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		a.render(w, "login", loginVM{IsAdmin: false, PublicMode: publicMode, FaviconURL: favicon})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	clientKey := a.clientIP(r)
	now := time.Now()
	if locked, retryAfter := a.loginLimiter.isLocked(clientKey, now); locked {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
		a.renderStatus(w, "login", loginVM{IsAdmin: false, PublicMode: publicMode, FaviconURL: favicon, Error: loginLockedMessage(retryAfter)}, http.StatusTooManyRequests)
		return
	}

	hash, ok, err := getSetting(a.db, settingPasswordHash)
	if err != nil || !ok {
		a.serverError(w, errors.New("server not initialized"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("password"))) != nil {
		if locked, retryAfter := a.loginLimiter.recordFailure(clientKey, now); locked {
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			a.renderStatus(w, "login", loginVM{IsAdmin: false, PublicMode: publicMode, FaviconURL: favicon, Error: loginLockedMessage(retryAfter)}, http.StatusTooManyRequests)
			return
		}
		a.render(w, "login", loginVM{IsAdmin: false, PublicMode: publicMode, FaviconURL: favicon, Error: "密码不正确"})
		return
	}
	a.loginLimiter.clear(clientKey)
	if err := a.setSessionCookie(w, r); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
