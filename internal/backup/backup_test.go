package backup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mb/internal/asset"
	"mb/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.CreateDomain(ctx, asset.Domain{
		Name: "example.com", Registrar: "R", ExpiresAt: "2027-01-01", Price: 12.5, Currency: "USD",
		Notes: "note", Pinned: true, Hidden: true, ExcludeCost: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVPS(ctx, asset.VPS{
		Vendor: "Acme", IP: "192.0.2.1", Region: "HK", Cores: 2, MemoryGB: 4,
		Disks:      []asset.Disk{{SizeGB: 80, Type: "SSD"}},
		TrafficSet: true, TrafficTB: 0, TrafficDir: asset.TrafficSingle,
		BandwidthValue: 1, BandwidthUnit: asset.UnitGbps,
		ExpiresAt: asset.Permanent, Price: 5, Currency: "EUR", BillingMonths: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMany(ctx, map[string]string{
		store.KeyCookieSecret: "source-secret",
		store.KeyPasswordHash: "source-hash",
		"public_mode":         "1",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExportRestoreRoundTrip(t *testing.T) {
	source := openStore(t)
	seed(t, source)
	ctx := context.Background()

	data, err := Export(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "source-secret") {
		t.Fatal("the cookie signing secret must never be exported")
	}

	target := openStore(t)
	if err := target.SetMany(ctx, map[string]string{store.KeyCookieSecret: "target-secret", "stale": "x"}); err != nil {
		t.Fatal(err)
	}
	payload, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, target, payload, true); err != nil {
		t.Fatal(err)
	}

	domains, _ := target.Domains(ctx)
	if len(domains) != 1 {
		t.Fatalf("domains = %+v", domains)
	}
	d := domains[0]
	if d.Name != "example.com" || d.Price != 12.5 || d.Currency != "USD" || !d.Pinned || !d.Hidden || !d.ExcludeCost {
		t.Errorf("domain did not survive the round trip: %+v", d)
	}
	servers, _ := target.VPSList(ctx)
	if len(servers) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	v := servers[0]
	if v.Vendor != "Acme" || len(v.Disks) != 1 || v.Disks[0].Type != "SSD" || !v.TrafficSet || v.TrafficTB != 0 ||
		v.TrafficDir != asset.TrafficSingle || v.BandwidthUnit != asset.UnitGbps || v.BillingMonths != 1 || !asset.IsPermanent(v.ExpiresAt) {
		t.Errorf("VPS did not survive the round trip: %+v", v)
	}

	// 设置被整体替换,但当前实例的签名密钥保留。
	if got := target.String(store.KeyCookieSecret, ""); got != "target-secret" {
		t.Errorf("cookie secret = %q, want the target's own secret", got)
	}
	if got := target.String(store.KeyPasswordHash, ""); got != "source-hash" {
		t.Errorf("password hash = %q, want the one from the backup", got)
	}
	if _, ok := target.Get("stale"); ok || !target.Bool("public_mode", false) {
		t.Errorf("settings were not replaced: %v", target.Settings())
	}
}

func TestRestoreWithoutSettingsKeepsSettings(t *testing.T) {
	source := openStore(t)
	seed(t, source)
	ctx := context.Background()
	data, _ := Export(ctx, source)
	payload, _ := Decode(data)

	target := openStore(t)
	if err := target.Set(ctx, "mine", "kept"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, target, payload, false); err != nil {
		t.Fatal(err)
	}
	if target.String("mine", "") != "kept" || target.Bool("public_mode", false) {
		t.Errorf("settings must be untouched: %v", target.Settings())
	}
}

// 早期版本导出的备份:硬盘在 disk_gb / disk2_gb 里,带宽是整数 Mbps,没有 traffic_set。
const legacyBackup = `{
  "version": 1,
  "domains": [{"domain": "old.com", "expires_at": "永久", "renewal_price": 10, "currency": "???"}],
  "vps": [{"vendor": "Old", "disk_gb": 40, "disk_type": "ssd", "disk2_gb": 500, "traffic_tb": 2,
           "bandwidth_mbps": 100, "expires_at": "2027-05-05", "renewal_price": -3, "currency": "usd"}]
}`

func TestRestoreLegacyBackup(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	payload, err := Decode([]byte(legacyBackup))
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, st, payload, false); err != nil {
		t.Fatal(err)
	}

	domains, _ := st.Domains(ctx)
	if len(domains) != 1 || !asset.IsPermanent(domains[0].ExpiresAt) || domains[0].Currency != "CNY" || domains[0].SortOrder != 1 {
		t.Errorf("legacy domain: %+v", domains)
	}
	servers, _ := st.VPSList(ctx)
	if len(servers) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	v := servers[0]
	if len(v.Disks) != 2 || v.Disks[0] != (asset.Disk{SizeGB: 40, Type: "SSD"}) || !v.TrafficSet || v.TrafficTB != 2 ||
		v.BandwidthValue != 100 || v.Price != 0 || v.Currency != "USD" || v.BillingMonths != 12 {
		t.Errorf("legacy VPS: %+v", v)
	}
}

func TestRestoreRejectsBadData(t *testing.T) {
	st := openStore(t)
	seed(t, st)
	ctx := context.Background()

	bad := []string{
		`not json`,
		`{"version": 99, "domains": [], "vps": []}`,
		`{"domains": [{"domain": "", "expires_at": "2027-01-01"}], "vps": []}`,
		`{"domains": [{"domain": "a.com", "expires_at": "someday"}], "vps": []}`,
		`{"domains": [], "vps": [{"vendor": "V", "expires_at": ""}]}`,
	}
	for _, raw := range bad {
		payload, err := Decode([]byte(raw))
		if err == nil {
			err = Restore(ctx, st, payload, false)
		}
		if err == nil {
			t.Errorf("backup should be rejected: %s", raw)
		}
	}
	// 被拒绝的备份不应改动现有数据。
	if domains, _ := st.Domains(ctx); len(domains) != 1 || domains[0].Name != "example.com" {
		t.Errorf("existing data was modified: %+v", domains)
	}

	payload, _ := Decode([]byte(`{"domains": [], "vps": []}`))
	if err := Restore(ctx, st, payload, true); err == nil {
		t.Error("restoring settings from a backup without settings should fail")
	}
}

func TestExportIsCompatibleWithOlderReaders(t *testing.T) {
	st := openStore(t)
	seed(t, st)
	data, _ := Export(context.Background(), st)

	// 旧版程序把这些字段读成整数和字符串,格式不能变。
	var raw struct {
		Domains []map[string]any `json:"domains"`
		VPS     []map[string]any `json:"vps"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Domains[0]["pinned"] != float64(1) || raw.VPS[0]["traffic_set"] != float64(1) {
		t.Errorf("flags must be exported as 0/1: %v %v", raw.Domains[0]["pinned"], raw.VPS[0]["traffic_set"])
	}
	if disks, ok := raw.VPS[0]["disks_json"].(string); !ok || !strings.Contains(disks, `"size_gb":80`) {
		t.Errorf("disks_json must be a JSON string: %v", raw.VPS[0]["disks_json"])
	}
}

func TestWebDAVTarget(t *testing.T) {
	cases := []struct {
		url, filename, defaultName, want string
	}{
		{"https://dav.example.com/backup/", "", "auto.json", "https://dav.example.com/backup/auto.json"},
		{"https://dav.example.com/backup/", "mine.json", "auto.json", "https://dav.example.com/backup/mine.json"},
		{"https://dav.example.com/backup", "mine.json", "auto.json", "https://dav.example.com/backup/mine.json"},
		{"https://dav.example.com/backup/mb.json", "", "auto.json", "https://dav.example.com/backup/mb.json"},
		{"https://dav.example.com/backup/mb.json", "ignored.json", "", "https://dav.example.com/backup/mb.json"},
		{"https://s3.example.com/b/mb.json?X-Amz-Signature=abc", "", "", "https://s3.example.com/b/mb.json?X-Amz-Signature=abc"},
	}
	for _, c := range cases {
		got, err := WebDAV{URL: c.url, Filename: c.filename}.target(c.defaultName)
		if err != nil || got.String() != c.want {
			t.Errorf("target(%q, %q) = %v, %v; want %q", c.url, c.filename, got, err, c.want)
		}
	}

	invalid := []WebDAV{
		{},
		{URL: "ftp://example.com/x"},
		{URL: "https://dav.example.com/backup/"}, // 下载时目录必须配文件名
		{URL: "https://dav.example.com/backup/", Filename: "../escape.json"},
		{URL: "https://s3.example.com/b/?X-Amz-Signature=abc"},
		{URL: "https://s3.example.com/b/mb.json?sig=abc", Filename: "extra.json"},
	}
	for _, w := range invalid {
		if got, err := w.target(""); err == nil {
			t.Errorf("target(%+v) = %v, want an error", w, got)
		}
	}
}

// davServer 是一个要求认证的最小 WebDAV 服务器:支持 PUT / GET 一个文件。
func davServer(t *testing.T, digest bool) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	files := map[string][]byte{}
	var authorizations []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth := r.Header.Get("Authorization")
		authorizations = append(authorizations, auth)

		authorized := false
		if digest {
			// 按 RFC 7616 独立计算一遍 response,验证客户端的 Digest 实现。
			_, params, _ := strings.Cut(auth, " ")
			p := parseAuthParams(params)
			ha1 := md5Hex("user:dav:pass")
			ha2 := md5Hex(r.Method + ":" + r.URL.RequestURI())
			want := md5Hex(strings.Join([]string{ha1, "abc123", p["nc"], p["cnonce"], "auth", ha2}, ":"))
			authorized = strings.HasPrefix(auth, "Digest ") && p["username"] == "user" && p["response"] == want
		} else {
			user, pass, ok := r.BasicAuth()
			authorized = ok && user == "user" && pass == "pass"
		}
		if !authorized {
			if digest {
				w.Header().Set("WWW-Authenticate", `Digest realm="dav", nonce="abc123", qop="auth", algorithm=MD5`)
			} else {
				w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodPut:
			files[r.URL.Path], _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			data, ok := files[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		}
	}))
	t.Cleanup(server.Close)
	return server, &authorizations
}

func TestWebDAVUploadDownload(t *testing.T) {
	for _, digest := range []bool{false, true} {
		server, authorizations := davServer(t, digest)
		dav := WebDAV{URL: server.URL + "/backup/", Filename: "mb.json", Username: "user", Password: "pass"}
		ctx := context.Background()

		if err := dav.Upload(ctx, []byte(`{"hello":"world"}`)); err != nil {
			t.Fatalf("digest=%v upload: %v", digest, err)
		}
		data, err := dav.Download(ctx)
		if err != nil || string(data) != `{"hello":"world"}` {
			t.Fatalf("digest=%v download = %q, %v", digest, data, err)
		}
		if !digest && len(*authorizations) != 2 {
			t.Errorf("basic auth should be sent with the first request, saw %d requests", len(*authorizations))
		}

		wrong := dav
		wrong.Password = "nope"
		if err := wrong.Upload(ctx, []byte("{}")); err == nil {
			t.Errorf("digest=%v: wrong password should fail", digest)
		}
		missing := dav
		missing.Filename = "absent.json"
		if _, err := missing.Download(ctx); err == nil {
			t.Errorf("digest=%v: missing file should fail", digest)
		}
	}
}
