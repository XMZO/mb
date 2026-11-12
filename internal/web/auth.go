package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mb/internal/store"
)

const (
	sessionTTL = 7 * 24 * time.Hour
	csrfTTL    = 30 * 24 * time.Hour

	minPasswordLen = 8
	maxPasswordLen = 72 // bcrypt 只处理前 72 个字节

	csrfField  = "csrf_token"
	csrfHeader = "X-CSRF-Token"

	// 同一把密钥签出的不同用途的值互不通用。
	purposeSession = "session"
	purposeCSRF    = "csrf"
)

// Bootstrap 确保 Cookie 签名密钥与管理员密码存在,在启动时以及恢复备份后调用。
//
//   - 设置了 MB_ADMIN_PASSWORD:以环境变量为准,与库内不一致时同步过去;
//   - 未设置且库内没有密码:生成随机密码并打印到日志(仅此一次);
//   - 未设置且库内已有密码:保持不变,由"设置"页管理。
func Bootstrap(ctx context.Context, st *store.Store, envPassword string) error {
	if secret, _ := st.Get(store.KeyCookieSecret); secret == "" {
		if err := st.Set(ctx, store.KeyCookieSecret, randomToken(32)); err != nil {
			return err
		}
	}

	hash, _ := st.Get(store.KeyPasswordHash)
	switch {
	case envPassword != "":
		if hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(envPassword)) == nil {
			return nil
		}
		if err := setPassword(ctx, st, envPassword); err != nil {
			return fmt.Errorf("apply MB_ADMIN_PASSWORD: %w", err)
		}
		log.Printf("auth: admin password synced from MB_ADMIN_PASSWORD")

	case hash == "":
		password := randomToken(15)
		if err := setPassword(ctx, st, password); err != nil {
			return err
		}
		log.Printf("============================================================")
		log.Printf(" 未设置 MB_ADMIN_PASSWORD,已生成随机管理员密码(仅显示这一次):")
		log.Printf("     %s", password)
		log.Printf(" 请用它登录并尽快在「设置」中修改;或在 .env 里设置 MB_ADMIN_PASSWORD。")
		log.Printf("============================================================")
	}
	return nil
}

// setPassword 保存新密码,同时更换 Cookie 签名密钥,让此前签发的所有会话立即失效。
func setPassword(ctx context.Context, st *store.Store, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return st.SetMany(ctx, map[string]string{
		store.KeyPasswordHash: string(hash),
		store.KeyCookieSecret: randomToken(32),
	})
}

func (s *Server) checkPassword(password string) bool {
	hash, _ := s.store.Get(store.KeyPasswordHash)
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ---- 会话 ----
//
// 会话是无状态的:Cookie 的内容是过期时间戳加上 HMAC 签名。

func (s *Server) secret() string {
	secret, _ := s.store.Get(store.KeyCookieSecret)
	return secret
}

func (s *Server) isAdmin(r *http.Request) bool {
	_, ok := s.sessionExpiry(r)
	return ok
}

func (s *Server) sessionExpiry(r *http.Request) (time.Time, bool) {
	c, err := r.Cookie(s.cfg.CookieName)
	if err != nil {
		return time.Time{}, false
	}
	payload, ok := verifySigned(purposeSession, c.Value, s.secret())
	if !ok {
		return time.Time{}, false
	}
	unix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	expires := time.Unix(unix, 0)
	return expires, time.Now().Before(expires)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	expires := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    signValue(purposeSession, strconv.FormatInt(expires.Unix(), 10), s.secret()),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
}

// renewSession 在会话有效期过半时续期,经常使用面板的管理员不会被定期踢下线。
func (s *Server) renewSession(w http.ResponseWriter, r *http.Request) {
	if expires, ok := s.sessionExpiry(r); ok && time.Until(expires) < sessionTTL/2 {
		s.startSession(w, r)
	}
}

func (s *Server) endSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---- CSRF ----
//
// 采用"签名的双重提交 Cookie":token 存在 HttpOnly Cookie 里,同时渲染进页面;
// 提交时页面带回 token(表单字段或请求头),与 Cookie 中的一致才放行。

func (s *Server) csrfCookieName() string { return s.cfg.CookieName + "_csrf" }

func (s *Server) readCSRF(r *http.Request) (string, bool) {
	c, err := r.Cookie(s.csrfCookieName())
	if err != nil {
		return "", false
	}
	return verifySigned(purposeCSRF, c.Value, s.secret())
}

// csrfToken 返回当前浏览器的 CSRF token,没有时签发新的。
// 每次渲染页面都会重写 Cookie 以顺延有效期,长时间开着页面也不会因 token 过期而提交失败。
func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) string {
	token, ok := s.readCSRF(r)
	if !ok {
		token = randomToken(32)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.csrfCookieName(),
		Value:    signValue(purposeCSRF, token, s.secret()),
		Path:     "/",
		MaxAge:   int(csrfTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
	return token
}

func (s *Server) validCSRF(r *http.Request, provided string) bool {
	token, ok := s.readCSRF(r)
	return ok && provided != "" && hmac.Equal([]byte(provided), []byte(token))
}

// ---- 签名 ----

func signValue(purpose, payload, secret string) string {
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac(purpose, payload, secret))
}

func verifySigned(purpose, value, secret string) (payload string, ok bool) {
	i := strings.LastIndexByte(value, '.')
	if i <= 0 || secret == "" {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(value[i+1:])
	if err != nil || !hmac.Equal(signature, mac(purpose, value[:i], secret)) {
		return "", false
	}
	return value[:i], true
}

func mac(purpose, payload, secret string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(purpose))
	h.Write([]byte{0})
	h.Write([]byte(payload))
	return h.Sum(nil)
}

func randomToken(bytes int) string {
	b := make([]byte, bytes)
	_, _ = rand.Read(b) // crypto/rand 失败时会直接终止进程,不会返回错误
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---- 登录 / 退出 ----

type loginPage struct {
	base
	Error string
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.isAdmin(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", &loginPage{base: base{Title: "登录"}})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.isAdmin(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	fail := func(status int, message string) {
		s.render(w, r, status, "login", &loginPage{base: base{Title: "登录"}, Error: message})
	}
	tooMany := func(wait time.Duration) {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		fail(http.StatusTooManyRequests, lockedMessage(wait))
	}

	client := s.clientIP(r)
	now := time.Now()
	if wait := s.limiter.lockedFor(client, now); wait > 0 {
		tooMany(wait)
		return
	}
	if !s.checkPassword(r.PostForm.Get("password")) {
		if wait := s.limiter.fail(client, now); wait > 0 {
			tooMany(wait)
			return
		}
		fail(http.StatusUnauthorized, "密码不正确")
		return
	}

	s.limiter.reset(client)
	s.startSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func lockedMessage(wait time.Duration) string {
	if wait < time.Minute {
		return "登录尝试过多，请稍后再试。"
	}
	return fmt.Sprintf("登录尝试过多，请约 %d 分钟后再试。", int(math.Ceil(wait.Minutes())))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.endSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
