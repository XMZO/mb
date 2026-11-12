package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mb/internal/asset"
	"mb/internal/config"
	"mb/internal/fx"
	"mb/internal/mailer"
	"mb/internal/store"
)

const testPassword = "correct horse battery"

// testApp 是跑在 httptest 上的完整应用,client 带 Cookie 且不自动跟随重定向。
type testApp struct {
	t      *testing.T
	store  *store.Store
	server *Server
	http   *httptest.Server
	client *http.Client
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := setPassword(ctx, st, testPassword); err != nil {
		t.Fatal(err)
	}
	rates := fx.New(st)
	if err := rates.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rates.SetOverrides(ctx, map[string]float64{"USD": 1.25, "CNY": 10}); err != nil { // 1 USD = 8 CNY
		t.Fatal(err)
	}
	if err := rates.SetAutoUpdate(ctx, false); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		CookieName: "mb_admin",
		Login:      config.LoginLimit{MaxFailures: 3, Window: time.Minute, Lockout: time.Minute, MaxEntries: 100},
	}
	server, err := New(cfg, st, rates)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	jar, _ := cookiejar.New(nil)
	t.Cleanup(func() {
		ts.Close()
		st.Close()
	})
	return &testApp{
		t: t, store: st, server: server, http: ts,
		client: &http.Client{
			Jar:           jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (a *testApp) do(req *http.Request) (*http.Response, string) {
	a.t.Helper()
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (a *testApp) get(path string) (*http.Response, string) {
	a.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.http.URL+path, nil)
	return a.do(req)
}

var csrfMeta = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// csrf 从登录页取出当前浏览器的 CSRF token(顺带拿到 CSRF Cookie)。
func (a *testApp) csrf() string {
	a.t.Helper()
	_, body := a.get("/healthz-does-not-exist") // 任何页面都带 token,用 404 页最便宜
	match := csrfMeta.FindStringSubmatch(body)
	if match == nil {
		a.t.Fatalf("no CSRF token in page:\n%s", body)
	}
	return match[1]
}

func (a *testApp) post(path string, form url.Values) (*http.Response, string) {
	a.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("csrf_token") {
		form.Set("csrf_token", a.csrf())
	}
	req, _ := http.NewRequest(http.MethodPost, a.http.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return a.do(req)
}

func (a *testApp) postJSON(path string, payload any) (*http.Response, string) {
	a.t.Helper()
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, a.http.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, a.csrf())
	return a.do(req)
}

func (a *testApp) login() {
	a.t.Helper()
	resp, body := a.post("/login", url.Values{"password": {testPassword}})
	if resp.StatusCode != http.StatusSeeOther {
		a.t.Fatalf("login failed: %d\n%s", resp.StatusCode, body)
	}
}

func (a *testApp) setPublic(values map[string]string) {
	a.t.Helper()
	if err := a.store.SetMany(context.Background(), values); err != nil {
		a.t.Fatal(err)
	}
}

func (a *testApp) addDomain(d asset.Domain) int64 {
	a.t.Helper()
	if d.Currency == "" {
		d.Currency = "CNY"
	}
	id, err := a.store.CreateDomain(context.Background(), d)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

func inDays(n int) string { return time.Now().AddDate(0, 0, n).Format(asset.DateLayout) }

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("page should contain %q", want)
		}
	}
}

func mustNotContain(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("page must not contain %q", secret)
		}
	}
}

// ---- 鉴权 ----

func TestPrivateModeRequiresLogin(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/", "/admin/settings", "/domains/new", "/vps/1/edit", "/admin/backup/export"} {
		resp, _ := app.get(path)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Errorf("GET %s = %d -> %q, want redirect to /login", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if resp, body := app.get("/login"); resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="password"`) {
		t.Errorf("login page: %d", resp.StatusCode)
	}

	app.login()
	if resp, body := app.get("/"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "新增域名") {
		t.Errorf("dashboard after login: %d", resp.StatusCode)
	}

	app.post("/logout", nil)
	if resp, _ := app.get("/"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout GET / = %d, want redirect", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t)
	for i := 1; i <= 2; i++ {
		if resp, body := app.post("/login", url.Values{"password": {"wrong"}}); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "密码不正确") {
			t.Fatalf("attempt %d: status %d", i, resp.StatusCode)
		}
	}
	resp, _ := app.post("/login", url.Values{"password": {"wrong"}})
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("third failure: status %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	// 锁定期间,正确的密码也不能登录。
	if resp, _ := app.post("/login", url.Values{"password": {testPassword}}); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("login while locked: status %d", resp.StatusCode)
	}
}

func TestCSRFProtection(t *testing.T) {
	app := newTestApp(t)
	app.login()

	if resp, _ := app.post("/logout", url.Values{"csrf_token": {"forged"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("form with a forged token: status %d, want 403", resp.StatusCode)
	}
	if resp, _ := app.get("/"); resp.StatusCode != http.StatusOK {
		t.Error("the rejected logout must not have ended the session")
	}

	req, _ := http.NewRequest(http.MethodPost, app.http.URL+"/api/domain/batch", strings.NewReader(`{"ids":[1],"action":"delete"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, _ := app.do(req); resp.StatusCode != http.StatusForbidden {
		t.Errorf("API call without the CSRF header: status %d, want 403", resp.StatusCode)
	}
}

func TestSecurityHeadersAndStatic(t *testing.T) {
	app := newTestApp(t)
	resp, body := app.get("/login")
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("missing security headers")
	}
	// CSP 不放行内联脚本与样式,页面里也不能出现。
	for _, forbidden := range []string{"<style", " style=", " onclick=", "<script>"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("page contains inline %q, which the CSP would block", forbidden)
		}
	}

	asset := regexp.MustCompile(`/static/app\.js\?v=[0-9a-f]+`).FindString(body)
	if asset == "" {
		t.Fatal("page does not reference a fingerprinted app.js")
	}
	resp, script := app.get(asset)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || len(script) < 1000 {
		t.Errorf("app.js: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := app.get("/static/"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("directory listing should be disabled, got %d", resp.StatusCode)
	}
	if resp, _ := app.get("/no/such/page"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path = %d, want 404", resp.StatusCode)
	}
	if resp, body := app.get("/healthz"); resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Errorf("healthz = %d %q", resp.StatusCode, body)
	}
}

// ---- 公开模式下的可见范围 ----

func TestPublicModeHidesWhatVisitorsMustNotSee(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.addDomain(asset.Domain{Name: "visible.example", Registrar: "SecretRegistrar", ExpiresAt: inDays(10), Price: 100, Currency: "USD", Notes: "secret-note"})
	app.addDomain(asset.Domain{Name: "concealed.example", Registrar: "R", ExpiresAt: inDays(5), Hidden: true})
	if _, err := app.store.CreateVPS(ctx, asset.VPS{
		Vendor: "VisibleVendor", IP: "203.0.113.77", Region: "SecretRegion", Specs: "secret-specs", Cores: 2,
		ExpiresAt: inDays(400), Price: 10, Currency: "CNY", BillingMonths: 1, Notes: "vps-note",
	}); err != nil {
		t.Fatal(err)
	}

	app.setPublic(map[string]string{
		keyPublicMode:          "1",
		keyShowTotals:          "0",
		keyShowDomainRegistrar: "0",
		keyShowDomainNotes:     "0",
		keyShowDomainExpiry:    "0",
		keyShowVPSIP:           "0",
		keyShowVPSRegion:       "0",
		keyShowVPSSpecs:        "0",
		keyShowVPSExpiry:       "0",
		keyShowVPSNotes:        "0",
	})

	resp, body := app.get("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public dashboard: %d", resp.StatusCode)
	}
	mustContain(t, body, "visible.example", "VisibleVendor", "100 USD", "另有 1 个域名未公开")
	mustNotContain(t, body,
		"concealed.example",              // 整条隐藏的记录
		"SecretRegistrar", "secret-note", // 关闭展示的域名字段
		inDays(10), "10 天", inDays(400), "30 天内", "data-days=\"10\"", // 到期时间、到期标签与到期筛选
		"203.0.113.77", "SecretRegion", "secret-specs", "vps-note", "2C",
		"年度续费",               // 统计
		"新增域名", "data-admin", // 管理功能
	)

	// 管理员仍然能看到全部内容。
	app.login()
	_, adminBody := app.get("/")
	mustContain(t, adminBody, "concealed.example", "SecretRegistrar", "secret-note", "203.0.113.77", "SecretRegion",
		inDays(10), `class="due due-danger">10 天`, "30 天内", `title="公开模式下访客看不到">隐藏`,
		"年度续费", "≈ ￥800", // 100 USD 按测试汇率折合 800 CNY
		"年化 ≈ ￥120", // 月付 10 CNY
	)
}

func TestBaseCurrencySelection(t *testing.T) {
	app := newTestApp(t)
	app.login()
	app.addDomain(asset.Domain{Name: "a.example", ExpiresAt: inDays(100), Price: 80, Currency: "CNY"})

	resp, _ := app.get("/?base=usd")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("switching base currency should redirect to /, got %d", resp.StatusCode)
	}
	_, body := app.get("/")
	mustContain(t, body, "$10", `<option value="USD" selected>`)

	app.get("/?base=NOPE") // 非法币种被忽略
	if _, body := app.get("/"); !strings.Contains(body, `<option value="USD" selected>`) {
		t.Error("an invalid base currency must not change the selection")
	}
}

// ---- 资产表单 ----

func TestDomainFormFlow(t *testing.T) {
	app := newTestApp(t)
	app.login()
	ctx := context.Background()

	// 校验失败:回到表单并保留输入。
	resp, body := app.post("/domains/new", url.Values{
		"domain": {"example.com"}, "expires_at": {"not-a-date"}, "price": {"12.5"}, "currency": {"USD"}, "notes": {"keep me"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid form: status %d", resp.StatusCode)
	}
	mustContain(t, body, "到期时间格式", `value="example.com"`, "keep me")

	// 粘贴完整网址也能识别出域名。
	resp, _ = app.post("/domains/new", url.Values{
		"domain": {"https://Example.com/path?q=1"}, "registrar": {"R"}, "expires_at": {"2027-03-04"},
		"price": {"12.5"}, "currency": {"usd"}, "pinned": {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: status %d", resp.StatusCode)
	}
	domains, _ := app.store.Domains(ctx)
	if len(domains) != 1 || domains[0].Name != "Example.com" || domains[0].Price != 12.5 || domains[0].Currency != "USD" || !domains[0].Pinned {
		t.Fatalf("created domain: %+v", domains)
	}
	id := domains[0].ID
	editPath := "/domains/" + itoa(id) + "/edit"

	if _, body := app.get("/"); !strings.Contains(body, "已添加域名 Example.com") {
		t.Error("success message was not shown after the redirect")
	}
	if _, body := app.get("/"); strings.Contains(body, "已添加域名") {
		t.Error("flash message must only be shown once")
	}

	if _, body := app.get(editPath); !strings.Contains(body, `value="2027-03-04"`) {
		t.Error("edit form is not pre-filled")
	}
	resp, _ = app.post(editPath, url.Values{"domain": {"example.org"}, "permanent": {"1"}, "price": {"免费"}, "currency": {"CNY"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: status %d", resp.StatusCode)
	}
	d, _ := app.store.Domain(ctx, id)
	if d.Name != "example.org" || !asset.IsPermanent(d.ExpiresAt) || d.Price != 0 || d.Pinned {
		t.Errorf("updated domain: %+v", d)
	}

	if resp, _ := app.get("/domains/9999/edit"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("editing a missing domain = %d, want 404", resp.StatusCode)
	}
	if resp, _ := app.post("/domains/9999/edit", url.Values{"domain": {"x.com"}, "permanent": {"1"}, "currency": {"CNY"}}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("updating a missing domain = %d, want 404", resp.StatusCode)
	}
}

func TestVPSFormFlow(t *testing.T) {
	app := newTestApp(t)
	app.login()
	ctx := context.Background()

	form := url.Values{
		"vendor": {"Acme"}, "region": {"HK"}, "ip": {"192.0.2.9"}, "cores": {"2"}, "memory_gb": {"0.5"},
		"disk_gb": {"40", "", "500"}, "disk_type": {"SSD", "", "HDD"},
		"traffic_tb": {"0"}, "traffic_dir": {"double"}, "bandwidth_value": {"1"}, "bandwidth_unit": {"Gbps"},
		"expires_at": {"2027-01-31"}, "price": {"5"}, "currency": {"EUR"}, "billing_months": {"1"},
	}
	if resp, body := app.post("/vps/new", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create VPS: %d\n%s", resp.StatusCode, body)
	}
	servers, _ := app.store.VPSList(ctx)
	if len(servers) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	v := servers[0]
	if len(v.Disks) != 2 || v.Disks[1] != (asset.Disk{SizeGB: 500, Type: "HDD"}) || !v.TrafficSet || v.TrafficTB != 0 ||
		v.TrafficDir != asset.TrafficDouble || v.BandwidthUnit != asset.UnitGbps || v.BillingMonths != 1 || v.MemoryGB != 0.5 {
		t.Errorf("created VPS: %+v", v)
	}
	_, body := app.get("/vps/" + itoa(v.ID) + "/edit")
	mustContain(t, body, `value="Acme"`, `name="traffic_tb" value="0"`, `value="500"`)
	_, dashboard := app.get("/")
	mustContain(t, dashboard, "2C/0.5G", "40GB SSD", "不限流量 双向", "1Gbps", "月付")

	invalid := []struct {
		field, value, message string
	}{
		{"vendor", "", "厂商不能为空"},
		{"cores", "-1", "核心数"},
		{"disk_gb", "abc", "硬盘的容量"},
		{"traffic_dir", "sideways", "单双向"},
		{"billing_months", "0", "月数"},
		{"currency", "XXX", "币种"},
		{"price", "-5", "续费价格"},
	}
	for _, c := range invalid {
		bad := url.Values{}
		for k, vs := range form {
			bad[k] = vs
		}
		bad[c.field] = []string{c.value}
		resp, body := app.post("/vps/new", bad)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, c.message) {
			t.Errorf("%s=%q: status %d, want an error mentioning %q", c.field, c.value, resp.StatusCode, c.message)
		}
	}
}

// ---- 批量接口与导出 ----

func TestBatchAPI(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	a := app.addDomain(asset.Domain{Name: "a.example", ExpiresAt: "2026-01-31"})
	b := app.addDomain(asset.Domain{Name: "b.example", ExpiresAt: "2026-06-15"})

	if resp, _ := app.postJSON("/api/domain/batch", batchRequest{IDs: []int64{a}, Action: "delete"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous batch call = %d, want 401", resp.StatusCode)
	}
	app.login()

	call := func(kind string, req batchRequest) (int, string) {
		resp, body := app.postJSON("/api/"+kind+"/batch", req)
		return resp.StatusCode, body
	}
	expect := func(status int, kind string, req batchRequest) {
		t.Helper()
		if got, body := call(kind, req); got != status {
			t.Errorf("%s %+v = %d %s, want %d", kind, req, got, body, status)
		}
	}

	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{a, b}, Action: "extend", Value: "1"})
	if d, _ := app.store.Domain(ctx, a); d.ExpiresAt != "2026-02-28" {
		t.Errorf("extend: %q", d.ExpiresAt)
	}
	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{a}, Action: "renew", Value: "永久"})
	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{b}, Action: "pin"})
	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{b}, Action: "hide"})
	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{b}, Action: "exclude_cost"})
	da, _ := app.store.Domain(ctx, a)
	db, _ := app.store.Domain(ctx, b)
	if !asset.IsPermanent(da.ExpiresAt) || !db.Pinned || !db.Hidden || !db.ExcludeCost || db.ExpiresAt != "2026-07-15" {
		t.Errorf("after batch: a=%+v b=%+v", da, db)
	}

	expect(http.StatusBadRequest, "domain", batchRequest{IDs: []int64{a}, Action: "renew", Value: "someday"})
	expect(http.StatusBadRequest, "domain", batchRequest{IDs: []int64{a}, Action: "extend", Value: "0"})
	expect(http.StatusBadRequest, "domain", batchRequest{IDs: []int64{a}, Action: "billing_months", Value: "3"})
	expect(http.StatusBadRequest, "domain", batchRequest{IDs: []int64{a}, Action: "format-disk"})
	expect(http.StatusBadRequest, "domain", batchRequest{IDs: nil, Action: "delete"})
	expect(http.StatusNotFound, "printers", batchRequest{IDs: []int64{a}, Action: "delete"})
	if _, body := call("domain", batchRequest{IDs: []int64{a}, Action: "renew", Value: "someday"}); !strings.Contains(body, "到期时间") {
		t.Errorf("error message should explain the problem: %s", body)
	}

	// 排序:置顶的 b 仍在最前,其余按给定顺序。
	c := app.addDomain(asset.Domain{Name: "c.example", ExpiresAt: "2027-01-01"})
	if resp, _ := app.postJSON("/api/domain/order", map[string]any{"ids": []int64{c, a, b}}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder = %d", resp.StatusCode)
	}
	domains, _ := app.store.Domains(ctx)
	if domains[0].ID != b || domains[1].ID != c || domains[2].ID != a {
		t.Errorf("order after reorder: %v %v %v", domains[0].Name, domains[1].Name, domains[2].Name)
	}

	expect(http.StatusNoContent, "domain", batchRequest{IDs: []int64{a, c}, Action: "delete"})
	if domains, _ := app.store.Domains(ctx); len(domains) != 1 || domains[0].ID != b {
		t.Errorf("after delete: %+v", domains)
	}
}

func TestDomainExport(t *testing.T) {
	app := newTestApp(t)
	app.login()
	a := app.addDomain(asset.Domain{Name: "a.example", Registrar: "R1", ExpiresAt: "2027-01-01", Price: 9.9, Currency: "USD", Notes: "=1+1"})
	b := app.addDomain(asset.Domain{Name: "b.example", ExpiresAt: asset.Permanent})
	app.addDomain(asset.Domain{Name: "c.example", ExpiresAt: "2027-01-01"})

	resp, body := app.post("/domains/export", url.Values{"format": {"txt"}, "scope": {"all"}})
	if resp.StatusCode != http.StatusOK || body != "a.example\nb.example\nc.example\n" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), ".txt") {
		t.Errorf("txt export: %d %q", resp.StatusCode, body)
	}

	// 只导出指定的记录,并保持给定的顺序。
	ids := itoa(b) + "," + itoa(a) + ",999,abc"
	_, body = app.post("/domains/export", url.Values{"format": {"txt"}, "scope": {"ids"}, "ids": {ids}})
	if body != "b.example\na.example\n" {
		t.Errorf("export by ids = %q", body)
	}
	_, body = app.post("/domains/export", url.Values{"format": {"txt"}, "scope": {"ids"}, "ids": {""}})
	if strings.TrimSpace(body) != "" {
		t.Errorf("an empty selection must export nothing, got %q", body)
	}

	resp, body = app.post("/domains/export", url.Values{"format": {"csv"}, "scope": {"ids"}, "ids": {ids}})
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Errorf("csv content type = %q", resp.Header.Get("Content-Type"))
	}
	mustContain(t, body, "domain,registrar,expires_at", "b.example,,永久,0,CNY", "a.example,R1,2027-01-01,9.9,USD,'=1+1")
}

// ---- 设置 ----

func TestSettingsVisibilityAndAppearance(t *testing.T) {
	app := newTestApp(t)
	app.login()

	resp, _ := app.post("/admin/settings/visibility", url.Values{"public_mode": {"1"}, "show_domain_price": {"1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/settings#visibility" {
		t.Fatalf("save visibility: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	vis := app.server.publicVisibility()
	if !app.server.publicMode() || !vis.DomainPrice || vis.Totals || vis.DomainNotes || vis.VPSIP {
		t.Errorf("visibility after save: %+v", vis)
	}
	if _, body := app.get("/admin/settings"); !strings.Contains(body, "展示设置已保存") {
		t.Error("success message missing")
	}

	// 站点图标与展示设置在同一个表单里;图标地址不合法时整个表单不保存,并保留刚才的勾选。
	resp, body := app.post("/admin/settings/visibility", url.Values{"show_vps_ip": {"1"}, "favicon_url": {"http://insecure.example/icon.png"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Favicon 链接无效") ||
		!strings.Contains(body, `data-initial-panel="visibility"`) || !strings.Contains(body, `name="show_vps_ip" value="1" checked`) {
		t.Errorf("invalid favicon: %d", resp.StatusCode)
	}
	if !app.server.publicMode() || app.server.publicVisibility().VPSIP {
		t.Error("a rejected form must not change any setting")
	}
	app.post("/admin/settings/visibility", url.Values{"public_mode": {"1"}, "favicon_url": {"https://cdn.example/icon.png"}})
	if _, body := app.get("/admin/settings"); !strings.Contains(body, `<link rel="icon" href="https://cdn.example/icon.png">`) {
		t.Error("custom favicon is not used")
	}
}

func TestSettingsFX(t *testing.T) {
	app := newTestApp(t)
	app.login()

	resp, body := app.post("/admin/settings/fx", url.Values{"rate_USD": {"1.5"}, "rate_CNY": {""}, "new_code": {"TRY"}, "new_rate": {"abc"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "手动汇率应为正数") {
		t.Fatalf("invalid rate: %d", resp.StatusCode)
	}
	resp, _ = app.post("/admin/settings/fx", url.Values{
		"auto_update": {"1"}, "rate_USD": {"1.5"}, "rate_CNY": {""}, "new_code": {"try"}, "new_rate": {"40"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save fx: %d", resp.StatusCode)
	}
	state := app.server.fx.State()
	if !state.AutoUpdate || len(state.Overrides) != 2 || state.Overrides["USD"] != 1.5 || state.Overrides["TRY"] != 40 {
		t.Errorf("overrides after save: %+v", state.Overrides)
	}
}

func TestChangePasswordInvalidatesOtherSessions(t *testing.T) {
	app := newTestApp(t)
	app.login()

	// 另一台设备上的会话。
	other := newClientSharing(app)
	other.login()

	cases := []struct {
		form    url.Values
		message string
	}{
		{url.Values{"current_password": {"wrong"}, "new_password": {"new-password-1"}, "confirm_password": {"new-password-1"}}, "当前密码不正确"},
		{url.Values{"current_password": {testPassword}, "new_password": {"short"}, "confirm_password": {"short"}}, "至少 8 位"},
		{url.Values{"current_password": {testPassword}, "new_password": {"new-password-1"}, "confirm_password": {"different"}}, "不一致"},
	}
	for _, c := range cases {
		if resp, body := app.post("/admin/settings/password", c.form); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, c.message) {
			t.Errorf("want error %q, got status %d", c.message, resp.StatusCode)
		}
	}

	resp, _ := app.post("/admin/settings/password", url.Values{
		"current_password": {testPassword}, "new_password": {"new-password-1"}, "confirm_password": {"new-password-1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password: %d", resp.StatusCode)
	}
	if resp, _ := app.get("/admin/settings"); resp.StatusCode != http.StatusOK {
		t.Error("the browser that changed the password should stay logged in")
	}
	if resp, _ := other.get("/admin/settings"); resp.StatusCode != http.StatusSeeOther {
		t.Error("sessions on other devices must be invalidated")
	}
	if !app.server.checkPassword("new-password-1") || app.server.checkPassword(testPassword) {
		t.Error("password was not updated")
	}
}

// newClientSharing 返回连到同一个服务、但 Cookie 独立的另一个"浏览器"。
func newClientSharing(app *testApp) *testApp {
	jar, _ := cookiejar.New(nil)
	clone := *app
	clone.client = &http.Client{Jar: jar, CheckRedirect: app.client.CheckRedirect}
	return &clone
}

func TestPasswordManagedByEnvironment(t *testing.T) {
	app := newTestApp(t)
	app.server.cfg.AdminPassword = testPassword
	app.login()

	if _, body := app.get("/admin/settings"); !strings.Contains(body, "MB_ADMIN_PASSWORD") || strings.Contains(body, `name="new_password"`) {
		t.Error("the password form should be replaced by an explanation")
	}
	resp, _ := app.post("/admin/settings/password", url.Values{
		"current_password": {testPassword}, "new_password": {"new-password-1"}, "confirm_password": {"new-password-1"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity || !app.server.checkPassword(testPassword) {
		t.Errorf("password managed by the environment must not be changeable: %d", resp.StatusCode)
	}
}

func TestEmailSettings(t *testing.T) {
	app := newTestApp(t)
	app.login()

	var sent []mailer.Config
	app.server.sendMail = func(_ context.Context, cfg mailer.Config, _ mailer.Message) error {
		sent = append(sent, cfg)
		return nil
	}
	form := url.Values{
		"enabled": {"1"}, "days": {"30, 7"}, "to": {"me@example.com"}, "host": {"smtp.example.com"}, "port": {"465"},
		"encryption": {"ssl"}, "auth": {"login"}, "username": {"user@example.com"}, "password": {"smtp-secret"},
	}

	if resp, body := app.post("/admin/settings/email", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save email: %d\n%s", resp.StatusCode, body)
	}
	saved := mailer.LoadSettings(app.store)
	if saved.Password != "smtp-secret" || saved.Encryption != mailer.EncryptionSSL || saved.Auth != mailer.AuthLogin {
		t.Errorf("saved settings: %+v", saved)
	}
	// 密码不回显到页面上。
	_, body := app.get("/admin/settings")
	mustNotContain(t, body, "smtp-secret")
	mustContain(t, body, "已保存，留空则不修改", `value="7,30"`)

	// 留空密码表示不修改;"测试"会先保存再发信。
	form.Set("password", "")
	form.Set("action", "test")
	if resp, _ := app.post("/admin/settings/email", form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("test email: %d", resp.StatusCode)
	}
	if len(sent) != 1 || sent[0].Password != "smtp-secret" || sent[0].Port != 465 || sent[0].From.Address != "user@example.com" {
		t.Errorf("test email config: %+v", sent)
	}

	form.Set("clear_password", "1")
	form.Set("action", "save")
	app.post("/admin/settings/email", form)
	if mailer.LoadSettings(app.store).Password != "" {
		t.Error("password was not cleared")
	}

	// 启用提醒时,配置必须完整。
	incomplete := url.Values{"enabled": {"1"}, "days": {"7"}, "host": {""}, "to": {"me@example.com"}}
	if resp, body := app.post("/admin/settings/email", incomplete); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "邮件服务器地址") {
		t.Errorf("incomplete settings with reminders enabled: %d", resp.StatusCode)
	}
	bad := url.Values{"days": {"soon"}}
	if resp, body := app.post("/admin/settings/email", bad); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `value="soon"`) {
		t.Errorf("invalid days should be shown again for correction: %d", resp.StatusCode)
	}
}

// ---- 备份 ----

func TestBackupExportAndImport(t *testing.T) {
	app := newTestApp(t)
	app.login()
	ctx := context.Background()
	app.addDomain(asset.Domain{Name: "kept.example", ExpiresAt: "2027-01-01", Price: 3, Currency: "USD"})

	resp, exported := app.get("/admin/backup/export")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "mb-backup-") {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	if secret, _ := app.store.Get(store.KeyCookieSecret); strings.Contains(exported, secret) {
		t.Fatal("backup contains the cookie signing secret")
	}

	// 改动数据后,从备份恢复。
	app.addDomain(asset.Domain{Name: "extra.example", ExpiresAt: "2027-01-01"})
	app.setPublic(map[string]string{keyPublicMode: "1"})

	upload := func(content string, withSettings bool) (*http.Response, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("csrf_token", app.csrf())
		if withSettings {
			_ = w.WriteField("restore_settings", "1")
		}
		part, _ := w.CreateFormFile("backup_file", "backup.json")
		_, _ = part.Write([]byte(content))
		w.Close()
		req, _ := http.NewRequest(http.MethodPost, app.http.URL+"/admin/backup/import", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		return app.do(req)
	}

	if resp, body := upload("{broken", false); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "不是有效的 JSON") {
		t.Errorf("broken backup: %d", resp.StatusCode)
	}
	if domains, _ := app.store.Domains(ctx); len(domains) != 2 {
		t.Fatal("a rejected backup must not modify data")
	}

	if resp, body := upload(exported, true); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import: %d\n%s", resp.StatusCode, body)
	}
	domains, _ := app.store.Domains(ctx)
	if len(domains) != 1 || domains[0].Name != "kept.example" {
		t.Errorf("after import: %+v", domains)
	}
	if app.server.publicMode() {
		t.Error("settings were not restored")
	}
	// 恢复设置之后,当前浏览器仍然保持登录。
	if resp, body := app.get("/admin/settings"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "已从备份文件恢复：1 个域名、0 台 VPS，以及设置") {
		t.Errorf("after import: status %d", resp.StatusCode)
	}
}

// ---- 客户端地址 ----

func TestClientIP(t *testing.T) {
	request := func(remote string, headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	spoofed := map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7", "X-Real-IP": "6.6.6.6"}

	// 默认不信任任何转发头。
	direct := &Server{}
	if got := direct.clientIP(request("203.0.113.5:4321", spoofed)); got != "203.0.113.5" {
		t.Errorf("untrusted headers: %q", got)
	}

	// 信任代理:取最右侧(由代理追加)的地址,客户端自己填的最左侧地址无效。
	anyProxy := &Server{cfg: config.Config{TrustProxyHeaders: true}}
	if got := anyProxy.clientIP(request("10.0.0.1:1", spoofed)); got != "198.51.100.7" {
		t.Errorf("trusted proxy: %q", got)
	}
	if got := anyProxy.clientIP(request("10.0.0.1:1", map[string]string{"X-Real-IP": "198.51.100.8"})); got != "198.51.100.8" {
		t.Errorf("X-Real-IP fallback: %q", got)
	}

	// 限定可信网段:跳过链路上的可信代理;不是来自可信代理的请求,转发头一律无效。
	limited := &Server{cfg: config.Config{
		TrustProxyHeaders: true,
		TrustedProxies:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}}
	chain := map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7, 10.0.0.9"}
	if got := limited.clientIP(request("10.0.0.1:1", chain)); got != "198.51.100.7" {
		t.Errorf("proxy chain: %q", got)
	}
	if got := limited.clientIP(request("203.0.113.5:1", chain)); got != "203.0.113.5" {
		t.Errorf("request from an untrusted peer: %q", got)
	}
	if got := limited.clientIP(request("[::ffff:10.0.0.1]:1", chain)); got != "198.51.100.7" {
		t.Errorf("IPv4-mapped proxy address: %q", got)
	}
}

func TestBootstrap(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 首次启动且未指定密码:生成随机密码与签名密钥。
	if err := Bootstrap(ctx, st, ""); err != nil {
		t.Fatal(err)
	}
	firstHash, _ := st.Get(store.KeyPasswordHash)
	firstSecret, _ := st.Get(store.KeyCookieSecret)
	if firstHash == "" || firstSecret == "" {
		t.Fatal("password hash and cookie secret must be created")
	}

	// 再次启动:保持不变。
	if err := Bootstrap(ctx, st, ""); err != nil {
		t.Fatal(err)
	}
	if hash, _ := st.Get(store.KeyPasswordHash); hash != firstHash {
		t.Error("existing password must be kept")
	}

	// 指定了环境变量:同步密码并更换密钥(旧会话失效);再次启动不重复更换。
	if err := Bootstrap(ctx, st, "from-environment"); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st}
	secret, _ := st.Get(store.KeyCookieSecret)
	if !server.checkPassword("from-environment") || secret == firstSecret {
		t.Error("environment password was not applied")
	}
	if err := Bootstrap(ctx, st, "from-environment"); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Get(store.KeyCookieSecret); again != secret {
		t.Error("an unchanged environment password must not rotate the secret")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
