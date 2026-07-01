package mb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// newTestApp 用临时文件建一个真实的库(走生产的 openDB/migrate/ensureDefaults),
// 供写路径冒烟测试使用。
func newTestApp(t *testing.T) *app {
	t.Helper()
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "mb_test.db"))
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config{
		cookieName:    defaultCookieName,
		adminPassword: "test-password",
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := ensureDefaults(db, cfg); err != nil {
		t.Fatalf("ensureDefaults: %v", err)
	}
	return &app{
		cfg:          cfg,
		db:           db,
		templates:    mustTemplates(),
		loginLimiter: newLoginLimiter(defaultLoginMaxFailures, defaultLoginWindow, defaultLoginLockout, defaultLoginMaxEntries),
	}
}

// TestWriteStatementsSelfCheck 复用启动期自检:列数 vs 占位符数不一致会被 Prepare 抓出。
func TestWriteStatementsSelfCheck(t *testing.T) {
	a := newTestApp(t)
	if err := validateWriteStatements(a.db); err != nil {
		t.Fatalf("validateWriteStatements: %v", err)
	}
}

// TestApplyBackupRoundTrip 真实执行导入路径的 domains/vps INSERT,
// 占位符 / 列 / 参数任一错位都会在这里失败 —— 正是 P0-0 那类 bug 的守门测试。
func TestApplyBackupRoundTrip(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	payload := backupPayload{
		Version: backupVersion,
		Domains: []backupDomain{{
			Domain:       "example.com",
			Registrar:    "TestRegistrar",
			ExpiresAt:    "2030-01-01",
			RenewalPrice: 12.5,
			Currency:     "USD",
			Notes:        "domain-note",
			CreatedAt:    "2026-01-01T00:00:00Z",
			UpdatedAt:    "2026-01-01T00:00:00Z",
		}},
		VPS: []backupVPS{{
			Vendor:        "TestVendor",
			IP:            "203.0.113.7",
			Specs:         "1C1G",
			Region:        "us-east",
			Cores:         1,
			MemoryGB:      1,
			DiskGB:        20,
			DiskType:      "ssd",
			ExpiresAt:     "2030-06-01",
			RenewalPrice:  5,
			Currency:      "USD",
			BillingMonths: 12,
			Notes:         "vps-note",
			CreatedAt:     "2026-01-01T00:00:00Z",
			UpdatedAt:     "2026-01-01T00:00:00Z",
		}},
	}

	if err := a.applyBackup(ctx, payload, false); err != nil {
		t.Fatalf("applyBackup: %v", err)
	}

	got, err := a.buildBackup(ctx)
	if err != nil {
		t.Fatalf("buildBackup: %v", err)
	}
	if len(got.Domains) != 1 {
		t.Fatalf("domains round-trip: want 1, got %d", len(got.Domains))
	}
	if len(got.VPS) != 1 {
		t.Fatalf("vps round-trip: want 1, got %d", len(got.VPS))
	}
	if got.Domains[0].Domain != "example.com" {
		t.Errorf("domain mismatch: got %q", got.Domains[0].Domain)
	}
	if got.VPS[0].Vendor != "TestVendor" {
		t.Errorf("vendor mismatch: got %q", got.VPS[0].Vendor)
	}
}

// TestEnsureAdminPassword 覆盖 P0-1 的密码双模式:环境变量为准 / 随机生成 / 保留既有。
func TestEnsureAdminPassword(t *testing.T) {
	a := newTestApp(t) // newTestApp 用 adminPassword="test-password" 播种

	// 环境变量分支:库内密码应等于环境变量
	hash, ok, err := getSetting(a.db, settingPasswordHash)
	if err != nil || !ok {
		t.Fatalf("password hash missing: ok=%v err=%v", ok, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("test-password")) != nil {
		t.Fatalf("env password not seeded")
	}

	// 环境变量为准:改密码后应被同步
	if err := ensureAdminPassword(a.db, "new-secret"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	hash, _, _ = getSetting(a.db, settingPasswordHash)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-secret")) != nil {
		t.Fatalf("password not synced from env")
	}

	// 未设环境变量且已有密码:应保持不变
	if err := ensureAdminPassword(a.db, ""); err != nil {
		t.Fatalf("empty env: %v", err)
	}
	hash, _, _ = getSetting(a.db, settingPasswordHash)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-secret")) != nil {
		t.Fatalf("existing password should be preserved when env unset")
	}

	// 未设环境变量且库内无密码:应生成随机密码
	if _, err := a.db.Exec(`DELETE FROM settings WHERE key=?`, settingPasswordHash); err != nil {
		t.Fatalf("delete hash: %v", err)
	}
	if err := ensureAdminPassword(a.db, ""); err != nil {
		t.Fatalf("random: %v", err)
	}
	hash, ok, _ = getSetting(a.db, settingPasswordHash)
	if !ok || hash == "" {
		t.Fatalf("random password not generated")
	}
}

func TestLoginLimiterLockoutAndClear(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute, 10*time.Minute, 100)
	now := time.Unix(1000, 0)
	key := "198.51.100.10"

	for i := 0; i < 2; i++ {
		locked, retryAfter := limiter.recordFailure(key, now.Add(time.Duration(i)*time.Second))
		if locked || retryAfter != 0 {
			t.Fatalf("failure %d should not lock: locked=%v retry=%s", i+1, locked, retryAfter)
		}
	}
	locked, retryAfter := limiter.recordFailure(key, now.Add(2*time.Second))
	if !locked || retryAfter != 10*time.Minute {
		t.Fatalf("third failure should lock for 10m: locked=%v retry=%s", locked, retryAfter)
	}
	locked, retryAfter = limiter.isLocked(key, now.Add(3*time.Second))
	if !locked || retryAfter <= 0 {
		t.Fatalf("key should remain locked: locked=%v retry=%s", locked, retryAfter)
	}

	limiter.clear(key)
	locked, _ = limiter.isLocked(key, now.Add(4*time.Second))
	if locked {
		t.Fatalf("clear should remove lockout")
	}
}

func TestClientIPTrustProxyHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	req.Header.Set("X-Real-IP", "203.0.113.10")

	a := &app{}
	if got := a.clientIP(req); got != "198.51.100.20" {
		t.Fatalf("untrusted proxy headers should be ignored, got %q", got)
	}

	a.cfg.trustProxyHeaders = true
	if got := a.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted proxy should use first XFF ip, got %q", got)
	}

	a.cfg.trustedProxyCIDRs = parseTrustedProxyCIDRs("192.0.2.0/24")
	if got := a.clientIP(req); got != "198.51.100.20" {
		t.Fatalf("untrusted remote proxy CIDR should fall back to RemoteAddr, got %q", got)
	}

	a.cfg.trustedProxyCIDRs = parseTrustedProxyCIDRs("198.51.100.0/24")
	if got := a.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted remote proxy CIDR should use XFF, got %q", got)
	}
}

func TestHandleLoginRateLimit(t *testing.T) {
	a := newTestApp(t)
	a.loginLimiter = newLoginLimiter(2, time.Hour, 30*time.Minute, 100)

	remote := "198.51.100.30:12345"
	wrong := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"password": {"wrong-password"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		a.handleLogin(rr, req)
		return rr
	}

	if rr := wrong(); rr.Code != http.StatusOK {
		t.Fatalf("first wrong password should render login page, got %d", rr.Code)
	}
	rr := wrong()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second wrong password should be rate-limited, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatalf("rate-limited response should include Retry-After")
	}

	// 登录成功应清理此前失败计数(在未锁定的客户端上验证)。
	a.loginLimiter = newLoginLimiter(3, time.Hour, 30*time.Minute, 100)
	if rr := wrong(); rr.Code != http.StatusOK {
		t.Fatalf("wrong password before success should render login page, got %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"password": {"test-password"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remote
	rr = httptest.NewRecorder()
	a.handleLogin(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("correct password should redirect, got %d", rr.Code)
	}
	if len(a.loginLimiter.attempts) != 0 {
		t.Fatalf("successful login should clear failure state, got %d entries", len(a.loginLimiter.attempts))
	}
}

func TestServerErrorRedactsDetails(t *testing.T) {
	a := newTestApp(t)
	rr := httptest.NewRecorder()
	a.serverError(rr, errors.New("sqlite: secret/internal/path"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "sqlite") || strings.Contains(body, "secret/internal/path") {
		t.Fatalf("response leaked internal error details: %q", body)
	}
	if !strings.Contains(body, "服务器内部错误") {
		t.Fatalf("response should contain generic Chinese error, got %q", body)
	}
}
