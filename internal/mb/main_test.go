package mb

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"path/filepath"
	"regexp"
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
		cfg:           cfg,
		db:            db,
		templates:     mustTemplates(),
		loginLimiter:  newLoginLimiter(defaultLoginMaxFailures, defaultLoginWindow, defaultLoginLockout, defaultLoginMaxEntries),
		settingsCache: settingsCacheFor(db),
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

func TestCurrencySupportIncludesTRYAndTND(t *testing.T) {
	for _, code := range []string{"TRY", "TND", "GBP", "JPY", "ZAR"} {
		if !isSupportedCurrency(code) {
			t.Fatalf("expected %s to be supported", code)
		}
	}

	fx := fxInfo{Rates: map[string]float64{
		"EUR": 1,
		"USD": 1.2,
		"TRY": 40,
		"TND": 3.4,
	}}
	got := convertMoney(400, "TRY", "TND", fx)
	if got < 33.999 || got > 34.001 {
		t.Fatalf("TRY to TND conversion: want 34, got %v", got)
	}
}

func TestManualFxEditorKeepsTheSettingsPageCompact(t *testing.T) {
	featured := featuredManualFxCurrencies()
	wantFeatured := []string{"CNY", "USD", "TRY", "TND"}
	if len(featured) != len(wantFeatured) {
		t.Fatalf("featured manual currencies: want %d, got %d", len(wantFeatured), len(featured))
	}
	for i, want := range wantFeatured {
		if featured[i].Code != want {
			t.Fatalf("featured manual currency %d: want %s, got %s", i, want, featured[i].Code)
		}
	}
	if got, want := len(featured)+len(additionalManualFxCurrencies()), len(manualFxCurrencies()); got != want {
		t.Fatalf("manual currency groups: want %d total, got %d", want, got)
	}

	a := newTestApp(t)
	rr := httptest.NewRecorder()
	a.render(rr, "settings", a.buildSettingsVM(false, "", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("render settings: status %d", rr.Code)
	}
	body := rr.Body.String()
	directInputs := regexp.MustCompile(`name="fx_rate_[A-Z]{3}"`).FindAllString(body, -1)
	if len(directInputs) != len(wantFeatured) {
		t.Fatalf("direct manual rate inputs: want %d, got %d", len(wantFeatured), len(directInputs))
	}
	if !strings.Contains(body, `name="fx_rate_currency"`) || !strings.Contains(body, `value="MVR"`) {
		t.Fatalf("additional currency selector is missing")
	}
	for _, hook := range []string{`class="page-enter settings-page"`, `class="settings-workspace"`, `class="settings-toggle-grid"`, `data-settings-tab="appearance"`, `data-settings-panel="backup"`} {
		if !strings.Contains(body, hook) {
			t.Fatalf("settings workspace hook missing: %s", hook)
		}
	}
	if got := strings.Count(body, `data-settings-panel=`); got != 5 {
		t.Fatalf("settings panels: want 5, got %d", got)
	}
}

func TestSettingsCanSaveAnAdditionalManualFxRate(t *testing.T) {
	a := newTestApp(t)
	values := url.Values{
		"form":             {"access"},
		"fx_rate_currency": {"MVR"},
		"fx_rate_value":    {"17.75"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("save additional manual rate: status %d body=%s", rr.Code, rr.Body.String())
	}
	if got := a.getFxRates()["MVR"]; got != 17.75 {
		t.Fatalf("saved MVR rate: want 17.75, got %v", got)
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

func TestCSPAndCSRFRenderMeta(t *testing.T) {
	a := newTestApp(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	a.withCommon(http.HandlerFunc(a.handleLogin)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("login GET status: got %d", rr.Code)
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("missing CSP header")
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP should not contain unsafe-inline: %s", csp)
	}
	if !strings.Contains(csp, "'nonce-") || !strings.Contains(csp, "object-src 'none'") {
		t.Fatalf("CSP missing nonce or hardening directives: %s", csp)
	}
	body := rr.Body.String()
	if !regexp.MustCompile(`<style nonce="[^"]+">`).MatchString(body) {
		t.Fatalf("rendered page missing nonce on style tag")
	}
	if !regexp.MustCompile(`<script nonce="[^"]+">`).MatchString(body) {
		t.Fatalf("rendered page missing nonce on script tag")
	}
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Fatalf("rendered login form missing CSRF token")
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatalf("expected CSRF cookie to be set")
	}
}

func TestRenderedVersionMeta(t *testing.T) {
	oldVersion, oldCommit := appVersion, appCommit
	appVersion = "v9.8.7"
	appCommit = "1234567890abcdef"
	t.Cleanup(func() {
		appVersion, appCommit = oldVersion, oldCommit
	})

	a := newTestApp(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	a.withCommon(http.HandlerFunc(a.handleLogin)).ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `<span class="mono">v9.8.7</span>`) {
		t.Fatalf("rendered page missing app version: %q", body)
	}
	if !strings.Contains(body, `commit 1234567890ab`) {
		t.Fatalf("rendered page missing short commit: %q", body)
	}
}

func TestDashboardAdminFormsRenderCSRFInsideRows(t *testing.T) {
	templates := mustTemplates()
	vm := dashboardVM{
		RenderMeta: RenderMeta{
			CSPNonce:   "nonce-test",
			CSRFToken:  "csrf-test",
			AppVersion: "test",
		},
		IsAdmin:        true,
		Fx:             fxInfo{Rates: defaultFxRates(), Source: fxSourceManual},
		BaseCurrency:   "USD",
		BaseSymbol:     "$",
		ShowTotals:     true,
		Domains:        []domainRow{{ID: 1, Domain: "example.com", Registrar: "test", ExpiresAt: "2030-01-01", DaysLeft: 30, Currency: "USD", DaysBadgeClass: "text-bg-secondary"}},
		VPS:            []vpsRow{{ID: 1, Vendor: "test-vps", IP: "127.0.0.1", Region: "local", ExpiresAt: "2030-01-01", DaysLeft: 30, Currency: "USD", BillingMonths: 12, DaysBadgeClass: "text-bg-secondary"}},
		FaviconURL:     "",
		PublicMode:     false,
		VPSTotalBase:   0,
		GrandTotalBase: 0,
	}
	var buf bytes.Buffer
	if err := templates["dashboard"].ExecuteTemplate(&buf, "dashboard", vm); err != nil {
		t.Fatalf("dashboard render: %v", err)
	}
	if !strings.Contains(buf.String(), `value="csrf-test"`) {
		t.Fatalf("dashboard admin forms missing csrf token")
	}
	for _, hook := range []string{`class="fx-rates text-muted-2 small"`, `class="list-surface rounded-4`, `class="grid-card-head`, `class="card-actions`} {
		if !strings.Contains(buf.String(), hook) {
			t.Fatalf("dashboard mobile layout hook missing: %s", hook)
		}
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	a := newTestApp(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"password": {"wrong"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.withCommon(http.HandlerFunc(a.handleLogin)).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF should be rejected with 403, got %d", rr.Code)
	}
}

func TestCSRFAllowsValidToken(t *testing.T) {
	a := newTestApp(t)
	getRR := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	a.withCommon(http.HandlerFunc(a.handleLogin)).ServeHTTP(getRR, getReq)

	var csrfCookie *http.Cookie
	for _, c := range getRR.Result().Cookies() {
		if c.Name == csrfCookieName(a.cfg.cookieName) {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatalf("csrf cookie not set")
	}
	secret, ok, err := getSetting(a.db, settingCookieSecret)
	if err != nil || !ok {
		t.Fatalf("cookie secret missing: ok=%v err=%v", ok, err)
	}
	token, ok := validateSignedToken(csrfCookie.Value, secret)
	if !ok || token == "" {
		t.Fatalf("csrf cookie invalid")
	}

	postRR := httptest.NewRecorder()
	form := url.Values{"password": {"wrong"}, csrfFieldName: {token}}
	postReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(csrfCookie)
	a.withCommon(http.HandlerFunc(a.handleLogin)).ServeHTTP(postRR, postReq)
	if postRR.Code == http.StatusForbidden {
		t.Fatalf("valid CSRF token should not be rejected")
	}
}

func TestSettingsCacheUpdatesAndInvalidates(t *testing.T) {
	a := newTestApp(t)
	if err := setSetting(a.db, "cache_test", "one"); err != nil {
		t.Fatalf("set one: %v", err)
	}
	got, ok, err := getSetting(a.db, "cache_test")
	if err != nil || !ok || got != "one" {
		t.Fatalf("get one: got=%q ok=%v err=%v", got, ok, err)
	}
	if _, err := a.db.Exec(`UPDATE settings SET value='two' WHERE key='cache_test'`); err != nil {
		t.Fatalf("manual update: %v", err)
	}
	got, ok, err = getSetting(a.db, "cache_test")
	if err != nil || !ok || got != "one" {
		t.Fatalf("cache should keep old value before invalidate: got=%q ok=%v err=%v", got, ok, err)
	}
	invalidateSettingsCache(a.db)
	got, ok, err = getSetting(a.db, "cache_test")
	if err != nil || !ok || got != "two" {
		t.Fatalf("cache should reload after invalidate: got=%q ok=%v err=%v", got, ok, err)
	}
}

func TestEmailAuthOptions(t *testing.T) {
	values := url.Values{
		"email_enabled":     {"1"},
		"email_days_before": {"30"},
		"email_to":          {"receiver@example.com"},
		"email_from":        {"sender@example.com"},
		"email_user":        {"sender@example.com"},
		"email_pass":        {"secret"},
		"email_host":        {"smtp.office365.com"},
		"email_port":        {"587"},
		"email_protocol":    {"smtp"},
		"email_encryption":  {"starttls"},
		"email_auth":        {"login"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	form, errMsg := parseEmailSettingsForm(req)
	if errMsg != "" {
		t.Fatalf("parseEmailSettingsForm: %s", errMsg)
	}
	cfg, err := buildEmailConfig(form)
	if err != nil {
		t.Fatalf("buildEmailConfig: %v", err)
	}
	if cfg.Auth != "login" {
		t.Fatalf("auth: want login, got %q", cfg.Auth)
	}

	auth := smtpAuth(cfg)
	proto, resp, err := auth.Start(&smtp.ServerInfo{Name: "smtp.office365.com", TLS: true})
	if err != nil {
		t.Fatalf("login auth Start: %v", err)
	}
	if proto != "LOGIN" || resp != nil {
		t.Fatalf("login auth Start: proto=%q resp=%q", proto, resp)
	}
	if got, err := auth.Next([]byte("Username:"), true); err != nil || string(got) != "sender@example.com" {
		t.Fatalf("login auth username: got=%q err=%v", got, err)
	}
	if got, err := auth.Next([]byte("Password:"), true); err != nil || string(got) != "secret" {
		t.Fatalf("login auth password: got=%q err=%v", got, err)
	}
}

func TestPlainSMTPAuthRequiresTLSUnlessExplicitNone(t *testing.T) {
	auth := smtpAuth(emailConfig{
		Host:       "smtp.example.com",
		Encryption: "auto",
		Auth:       "plain",
		Username:   "user",
		Password:   "pass",
	})
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: false}); err == nil {
		t.Fatalf("plain auth should reject non-TLS server unless encryption is explicitly none")
	}

	auth = smtpAuth(emailConfig{
		Host:       "smtp.example.com",
		Encryption: "none",
		Auth:       "plain",
		Username:   "user",
		Password:   "pass",
	})
	proto, resp, err := auth.Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: false})
	if err != nil {
		t.Fatalf("explicit none should allow plaintext auth: %v", err)
	}
	if proto != "PLAIN" || string(resp) != "\x00user\x00pass" {
		t.Fatalf("plain auth payload mismatch: proto=%q resp=%q", proto, resp)
	}
}
