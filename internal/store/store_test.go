package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"mb/internal/asset"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mb.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func mustCreateDomain(t *testing.T, st *Store, name, expires string) int64 {
	t.Helper()
	id, err := st.CreateDomain(context.Background(), asset.Domain{Name: name, ExpiresAt: expires, Currency: "CNY"})
	if err != nil {
		t.Fatalf("CreateDomain(%s): %v", name, err)
	}
	return id
}

func domainNames(t *testing.T, st *Store) []string {
	t.Helper()
	domains, err := st.Domains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(domains))
	for i, d := range domains {
		names[i] = d.Name
	}
	return names
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDomainCRUD(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	id, err := st.CreateDomain(ctx, asset.Domain{
		Name: "example.com", Registrar: "NameSilo", ExpiresAt: "2027-01-31",
		Price: 12.99, Currency: "USD", Notes: "main", Pinned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.Domain(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "example.com" || d.Price != 12.99 || !d.Pinned || d.Hidden || d.SortOrder != 1 || d.CreatedAt == "" {
		t.Fatalf("unexpected domain: %+v", d)
	}

	d.Registrar, d.Hidden, d.ExpiresAt = "Cloudflare", true, asset.Permanent
	if err := st.UpdateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Domain(ctx, id); got.Registrar != "Cloudflare" || !got.Hidden || !asset.IsPermanent(got.ExpiresAt) {
		t.Fatalf("update not applied: %+v", got)
	}

	if _, err := st.Domain(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing domain: err = %v, want ErrNotFound", err)
	}
	if err := st.UpdateDomain(ctx, asset.Domain{ID: 999, Name: "x", ExpiresAt: "2027-01-01"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing domain: err = %v, want ErrNotFound", err)
	}
}

func TestVPSRoundTrip(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	in := asset.VPS{
		Vendor: "Acme", IP: "192.0.2.1", Region: "HK", Specs: "CN2", Cores: 2, MemoryGB: 0.5,
		Disks:      []asset.Disk{{SizeGB: 20, Type: "SSD"}, {SizeGB: 500, Type: "HDD"}},
		TrafficSet: true, TrafficTB: 0, TrafficDir: asset.TrafficDouble,
		BandwidthValue: 1, BandwidthUnit: asset.UnitGbps,
		ExpiresAt: "2026-12-01", Price: 5, Currency: "EUR", BillingMonths: 1, Notes: "n",
	}
	id, err := st.CreateVPS(ctx, in)
	if err != nil {
		t.Fatalf("CreateVPS: %v", err) // 列数与占位符数不一致时会在这里暴露
	}
	got, err := st.VPS(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Vendor != "Acme" || len(got.Disks) != 2 || got.Disks[1].SizeGB != 500 ||
		!got.TrafficSet || got.TrafficDir != asset.TrafficDouble || got.BandwidthUnit != asset.UnitGbps ||
		got.BillingMonths != 1 || got.AnnualPrice() != 60 {
		t.Fatalf("unexpected VPS: %+v", got)
	}

	got.Disks, got.TrafficSet, got.BillingMonths = nil, false, 12
	if err := st.UpdateVPS(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := st.VPS(ctx, id)
	if len(again.Disks) != 0 || again.TrafficSet || again.TrafficDir != "" || again.BillingMonths != 12 {
		t.Fatalf("update not applied: %+v", again)
	}
}

func TestListOrderAndReorder(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	a := mustCreateDomain(t, st, "a.com", "2027-01-01")
	b := mustCreateDomain(t, st, "b.com", "2027-01-01")
	c := mustCreateDomain(t, st, "c.com", "2027-01-01")

	if got := domainNames(t, st); !equal(got, []string{"a.com", "b.com", "c.com"}) {
		t.Fatalf("initial order = %v", got)
	}
	// 只给出部分 ID(其中还有重复和不存在的):提到的排前面,其余保持原有相对顺序。
	if err := st.Reorder(ctx, asset.KindDomain, []int64{c, 12345, c}); err != nil {
		t.Fatal(err)
	}
	if got := domainNames(t, st); !equal(got, []string{"c.com", "a.com", "b.com"}) {
		t.Fatalf("after reorder = %v", got)
	}
	// 置顶的排在最前,不受排序号影响。
	if err := st.SetFlag(ctx, asset.KindDomain, []int64{b}, FlagPinned, true); err != nil {
		t.Fatal(err)
	}
	if got := domainNames(t, st); !equal(got, []string{"b.com", "c.com", "a.com"}) {
		t.Fatalf("after pin = %v", got)
	}
	if err := st.DeleteAssets(ctx, asset.KindDomain, []int64{a, c}); err != nil {
		t.Fatal(err)
	}
	if got := domainNames(t, st); !equal(got, []string{"b.com"}) {
		t.Fatalf("after delete = %v", got)
	}
	if err := st.SetFlag(ctx, asset.KindDomain, []int64{b}, Flag("id = id; --"), true); err == nil {
		t.Error("unknown flag must be rejected")
	}
}

func TestExpiryUpdates(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	jan := mustCreateDomain(t, st, "jan.com", "2026-01-31")
	forever := mustCreateDomain(t, st, "forever.com", asset.Permanent)

	if err := st.ExtendExpiry(ctx, asset.KindDomain, []int64{jan, forever}, 1); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Domain(ctx, jan); d.ExpiresAt != "2026-02-28" {
		t.Errorf("extended expiry = %q, want 2026-02-28", d.ExpiresAt)
	}
	if d, _ := st.Domain(ctx, forever); !asset.IsPermanent(d.ExpiresAt) {
		t.Errorf("permanent domain must stay permanent, got %q", d.ExpiresAt)
	}
	if err := st.SetExpiry(ctx, asset.KindDomain, []int64{jan, forever}, "2030-06-01"); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Domain(ctx, forever); d.ExpiresAt != "2030-06-01" {
		t.Errorf("SetExpiry not applied: %q", d.ExpiresAt)
	}
}

func TestSettingsPersistAcrossReopen(t *testing.T) {
	st, path := openTemp(t)
	ctx := context.Background()
	if err := st.SetMany(ctx, map[string]string{"a": "1", "b": "two"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "a", "0"); err != nil {
		t.Fatal(err)
	}
	if st.Bool("a", true) || st.String("b", "") != "two" || st.String("missing", "fallback") != "fallback" {
		t.Fatal("cached settings are wrong")
	}
	st.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Bool("a", true) || reopened.String("b", "") != "two" {
		t.Fatal("settings were not persisted")
	}
}

func TestReplaceAll(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	mustCreateDomain(t, st, "old.com", "2027-01-01")
	if err := st.Set(ctx, "keep", "yes"); err != nil {
		t.Fatal(err)
	}

	fresh := []asset.Domain{{Name: "new.com", ExpiresAt: "2028-01-01", Currency: "USD", SortOrder: 1, CreatedAt: "t", UpdatedAt: "t"}}
	if err := st.ReplaceAll(ctx, fresh, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := domainNames(t, st); !equal(got, []string{"new.com"}) {
		t.Fatalf("after replace = %v", got)
	}
	if st.String("keep", "") != "yes" {
		t.Error("settings must be kept when settings is nil")
	}

	if err := st.ReplaceAll(ctx, nil, nil, map[string]string{"only": "this"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("keep"); ok || st.String("only", "") != "this" {
		t.Errorf("settings were not replaced: %v", st.Settings())
	}
}

// TestUpgradeLegacyDatabase 用最早期的表结构建库,确认打开后数据被整理成当前结构。
func TestUpgradeLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	legacy := []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL, registrar TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL, renewal_price REAL NOT NULL DEFAULT 0, currency TEXT NOT NULL DEFAULT 'CNY',
			notes TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE vps (
			id INTEGER PRIMARY KEY AUTOINCREMENT, vendor TEXT NOT NULL, ip TEXT NOT NULL DEFAULT '',
			specs TEXT NOT NULL DEFAULT '', disk_gb INTEGER NOT NULL DEFAULT 0, disk_type TEXT NOT NULL DEFAULT '',
			disk2_gb INTEGER NOT NULL DEFAULT 0, disk2_type TEXT NOT NULL DEFAULT '',
			traffic_tb REAL NOT NULL DEFAULT 0, bandwidth_mbps INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT NOT NULL, renewal_price REAL NOT NULL DEFAULT 0, currency TEXT NOT NULL DEFAULT 'CNY',
			notes TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO settings VALUES ('public_mode', '1')`,
		`INSERT INTO domains(domain, expires_at, created_at, updated_at) VALUES
			('later.com', '2028-01-01', 't', 't'), ('sooner.com', '2027-01-01', 't', 't'), ('forever.com', ' 永久 ', 't', 't')`,
		`INSERT INTO vps(vendor, disk_gb, disk_type, disk2_gb, traffic_tb, bandwidth_mbps, expires_at, created_at, updated_at)
			VALUES ('Old', 40, 'ssd', 1000, 2, 100, '2027-05-05', 't', 't')`,
	}
	for _, stmt := range legacy {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if !st.Bool("public_mode", false) {
		t.Error("existing settings were lost")
	}
	// 没有排序号的旧记录按到期日补序号;"永久"被规范成统一的写法。
	if got := domainNames(t, st); !equal(got, []string{"sooner.com", "later.com", "forever.com"}) {
		t.Errorf("legacy domain order = %v", got)
	}
	domains, _ := st.Domains(ctx)
	if !asset.IsPermanent(domains[2].ExpiresAt) {
		t.Errorf("permanent token not canonicalized: %q", domains[2].ExpiresAt)
	}

	servers, err := st.VPSList(ctx)
	if err != nil || len(servers) != 1 {
		t.Fatalf("VPSList = %v, %v", servers, err)
	}
	v := servers[0]
	if len(v.Disks) != 2 || v.Disks[0] != (asset.Disk{SizeGB: 40, Type: "SSD"}) || v.Disks[1].SizeGB != 1000 {
		t.Errorf("legacy disks not folded: %+v", v.Disks)
	}
	if !v.TrafficSet || v.TrafficTB != 2 || v.BandwidthValue != 100 || v.BandwidthUnit != asset.UnitMbps || v.BillingMonths != 12 {
		t.Errorf("legacy VPS fields not backfilled: %+v", v)
	}

	// 升级后的库仍然可以正常写入(旧的多余列有默认值)。
	if _, err := st.CreateVPS(ctx, asset.VPS{Vendor: "New", ExpiresAt: "2027-01-01", Currency: "CNY", BillingMonths: 12}); err != nil {
		t.Fatalf("CreateVPS on upgraded database: %v", err)
	}
	// 再次打开不应重复迁移或报错。
	st.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen upgraded database: %v", err)
	}
	again.Close()
}

func TestReminderLog(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	records := []ReminderRecord{
		{Kind: asset.KindDomain, ID: 1, DaysBefore: 30, SentDate: "2026-11-01"},
		{Kind: asset.KindVPS, ID: 2, DaysBefore: 7, SentDate: "2026-11-10"},
	}
	if err := st.AddReminderLog(ctx, records); err != nil {
		t.Fatal(err)
	}
	if err := st.AddReminderLog(ctx, records[:1]); err != nil { // 重复记录被忽略
		t.Fatal(err)
	}
	got, err := st.ReminderLog(ctx, "2026-11-05")
	if err != nil || len(got) != 1 || got[0] != records[1] {
		t.Fatalf("ReminderLog since = %+v, %v", got, err)
	}
	if err := st.PruneReminderLog(ctx, "2026-11-05"); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.ReminderLog(ctx, ""); len(all) != 1 {
		t.Fatalf("after prune = %+v", all)
	}
}
