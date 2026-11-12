// Package backup 负责 JSON 备份的导出与恢复,以及通过 WebDAV 推送 / 拉取备份。
package backup

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"mb/internal/asset"
	"mb/internal/money"
	"mb/internal/store"
	"mb/internal/usererr"
)

const (
	// Version 是备份文件的格式版本。字段保持与历史版本兼容,新旧程序的备份可以互相导入。
	Version = 1

	// MaxBytes 是允许导入的备份文件大小上限。
	MaxBytes int64 = 10 << 20
)

// Payload 是备份文件的顶层结构。
type Payload struct {
	Version    int               `json:"version"`
	ExportedAt string            `json:"exported_at"`
	Settings   map[string]string `json:"settings,omitempty"`
	Domains    []Domain          `json:"domains"`
	VPS        []VPS             `json:"vps"`
}

// Domain 是备份中的一条域名记录。布尔值沿用历史格式,以 0/1 表示。
type Domain struct {
	Domain       string  `json:"domain"`
	Registrar    string  `json:"registrar"`
	ExpiresAt    string  `json:"expires_at"`
	RenewalPrice float64 `json:"renewal_price"`
	Currency     string  `json:"currency"`
	Notes        string  `json:"notes"`
	ExcludeCost  int     `json:"exclude_cost,omitempty"`
	Pinned       int     `json:"pinned,omitempty"`
	Hidden       int     `json:"hidden,omitempty"`
	SortOrder    int     `json:"sort_order,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// VPS 是备份中的一条 VPS 记录。
type VPS struct {
	Vendor         string  `json:"vendor"`
	IP             string  `json:"ip"`
	Specs          string  `json:"specs"`
	Region         string  `json:"region"`
	Cores          int     `json:"cores"`
	MemoryGB       float64 `json:"memory_gb"`
	DisksJSON      string  `json:"disks_json"`
	TrafficTB      float64 `json:"traffic_tb"`
	TrafficSet     int     `json:"traffic_set"`
	TrafficDir     string  `json:"traffic_direction"`
	BandwidthValue float64 `json:"bandwidth_value"`
	BandwidthUnit  string  `json:"bandwidth_unit"`
	ExpiresAt      string  `json:"expires_at"`
	RenewalPrice   float64 `json:"renewal_price"`
	Currency       string  `json:"currency"`
	Notes          string  `json:"notes"`
	ExcludeCost    int     `json:"exclude_cost,omitempty"`
	Pinned         int     `json:"pinned,omitempty"`
	Hidden         int     `json:"hidden,omitempty"`
	BillingMonths  int     `json:"billing_months,omitempty"`
	SortOrder      int     `json:"sort_order,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`

	// 以下字段只出现在早期版本的备份里,仅在导入时读取。
	LegacyDiskGB        int    `json:"disk_gb,omitempty"`
	LegacyDiskType      string `json:"disk_type,omitempty"`
	LegacyDisk2GB       int    `json:"disk2_gb,omitempty"`
	LegacyDisk2Type     string `json:"disk2_type,omitempty"`
	LegacyBandwidthMbps int    `json:"bandwidth_mbps,omitempty"`
}

// Filename 返回带时间戳的默认备份文件名。
func Filename(now time.Time) string {
	return "mb-backup-" + now.Format("20060102-150405") + ".json"
}

// Export 把全部资产与设置导出为 JSON。
//
// Cookie 签名密钥不会导出:拿到它就能伪造登录会话,不应该随备份文件流转。
func Export(ctx context.Context, st *store.Store) ([]byte, error) {
	domains, err := st.Domains(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := st.VPSList(ctx)
	if err != nil {
		return nil, err
	}

	payload := Payload{
		Version:    Version,
		ExportedAt: time.Now().Format(time.RFC3339),
		Settings:   st.Settings(),
		Domains:    make([]Domain, len(domains)),
		VPS:        make([]VPS, len(servers)),
	}
	delete(payload.Settings, store.KeyCookieSecret)

	for i, d := range domains {
		payload.Domains[i] = Domain{
			Domain: d.Name, Registrar: d.Registrar, ExpiresAt: d.ExpiresAt,
			RenewalPrice: d.Price, Currency: d.Currency, Notes: d.Notes,
			ExcludeCost: flag(d.ExcludeCost), Pinned: flag(d.Pinned), Hidden: flag(d.Hidden),
			SortOrder: d.SortOrder, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		}
	}
	for i, v := range servers {
		payload.VPS[i] = VPS{
			Vendor: v.Vendor, IP: v.IP, Specs: v.Specs, Region: v.Region,
			Cores: v.Cores, MemoryGB: v.MemoryGB, DisksJSON: asset.DisksJSON(v.Disks),
			TrafficTB: v.TrafficTB, TrafficSet: flag(v.TrafficSet), TrafficDir: v.TrafficDir,
			BandwidthValue: v.BandwidthValue, BandwidthUnit: v.BandwidthUnit,
			ExpiresAt: v.ExpiresAt, RenewalPrice: v.Price, Currency: v.Currency, Notes: v.Notes,
			ExcludeCost: flag(v.ExcludeCost), Pinned: flag(v.Pinned), Hidden: flag(v.Hidden),
			BillingMonths: v.BillingMonths, SortOrder: v.SortOrder,
			CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		}
	}
	return json.MarshalIndent(payload, "", "  ")
}

// Decode 解析并初步校验备份文件。
func Decode(data []byte) (Payload, error) {
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, usererr.New("备份文件不是有效的 JSON")
	}
	if payload.Version != 0 && payload.Version != Version {
		return payload, usererr.Newf("不支持的备份版本：%d", payload.Version)
	}
	return payload, nil
}

// Restore 用备份内容替换现有的域名与 VPS;withSettings 为 true 时同时替换设置项。
//
// 恢复设置时,当前的 Cookie 签名密钥始终保留(备份里的会被忽略),
// 备份里没有管理员密码时也保留当前密码,保证恢复之后仍然能登录。
func Restore(ctx context.Context, st *store.Store, payload Payload, withSettings bool) error {
	now := time.Now().Format(time.RFC3339)
	domains, err := payload.domains(now)
	if err != nil {
		return err
	}
	servers, err := payload.servers(now)
	if err != nil {
		return err
	}

	var settings map[string]string
	if withSettings {
		if len(payload.Settings) == 0 {
			return usererr.New("备份文件中不包含设置项，请取消勾选“恢复设置”后重试")
		}
		settings = make(map[string]string, len(payload.Settings)+2)
		for key, value := range payload.Settings {
			if strings.TrimSpace(key) != "" {
				settings[key] = value
			}
		}
		if secret, ok := st.Get(store.KeyCookieSecret); ok {
			settings[store.KeyCookieSecret] = secret
		} else {
			delete(settings, store.KeyCookieSecret)
		}
		if _, ok := settings[store.KeyPasswordHash]; !ok {
			if hash, ok := st.Get(store.KeyPasswordHash); ok {
				settings[store.KeyPasswordHash] = hash
			}
		}
	}
	return st.ReplaceAll(ctx, domains, servers, settings)
}

func (p Payload) domains(now string) ([]asset.Domain, error) {
	nextSort := 0
	for _, d := range p.Domains {
		nextSort = max(nextSort, d.SortOrder)
	}

	out := make([]asset.Domain, 0, len(p.Domains))
	for i, d := range p.Domains {
		name := strings.TrimSpace(d.Domain)
		if name == "" {
			return nil, usererr.Newf("第 %d 条域名记录缺少域名", i+1)
		}
		expires, err := asset.ParseExpiry(d.ExpiresAt)
		if err != nil {
			return nil, usererr.Newf("域名 %s 的到期时间无效", name)
		}
		sortOrder := d.SortOrder
		if sortOrder <= 0 {
			nextSort++
			sortOrder = nextSort
		}
		out = append(out, asset.Domain{
			Name: name, Registrar: strings.TrimSpace(d.Registrar), ExpiresAt: expires,
			Price: amount(d.RenewalPrice), Currency: currency(d.Currency), Notes: d.Notes,
			ExcludeCost: d.ExcludeCost != 0, Pinned: d.Pinned != 0, Hidden: d.Hidden != 0,
			SortOrder: sortOrder,
			CreatedAt: timestampOr(d.CreatedAt, now), UpdatedAt: timestampOr(d.UpdatedAt, now),
		})
	}
	return out, nil
}

func (p Payload) servers(now string) ([]asset.VPS, error) {
	nextSort := 0
	for _, v := range p.VPS {
		nextSort = max(nextSort, v.SortOrder)
	}

	out := make([]asset.VPS, 0, len(p.VPS))
	for i, v := range p.VPS {
		vendor := strings.TrimSpace(v.Vendor)
		if vendor == "" {
			return nil, usererr.Newf("第 %d 条 VPS 记录缺少厂商", i+1)
		}
		expires, err := asset.ParseExpiry(v.ExpiresAt)
		if err != nil {
			return nil, usererr.Newf("VPS %s 的到期时间无效", vendor)
		}
		sortOrder := v.SortOrder
		if sortOrder <= 0 {
			nextSort++
			sortOrder = nextSort
		}

		server := asset.VPS{
			Vendor: vendor, IP: strings.TrimSpace(v.IP), Region: strings.TrimSpace(v.Region), Specs: v.Specs,
			Cores: max(v.Cores, 0), MemoryGB: amount(v.MemoryGB), Disks: v.disks(),
			BandwidthValue: amount(v.BandwidthValue), BandwidthUnit: asset.NormalizeBandwidthUnit(v.BandwidthUnit),
			ExpiresAt: expires, Price: amount(v.RenewalPrice), Currency: currency(v.Currency),
			BillingMonths: v.BillingMonths, Notes: v.Notes,
			ExcludeCost: v.ExcludeCost != 0, Pinned: v.Pinned != 0, Hidden: v.Hidden != 0,
			SortOrder: sortOrder,
			CreatedAt: timestampOr(v.CreatedAt, now), UpdatedAt: timestampOr(v.UpdatedAt, now),
		}
		if v.TrafficSet != 0 || v.TrafficTB > 0 {
			server.TrafficSet = true
			server.TrafficTB = amount(v.TrafficTB)
			server.TrafficDir = asset.NormalizeTrafficDir(v.TrafficDir)
		}
		if server.BandwidthValue == 0 && v.LegacyBandwidthMbps > 0 {
			server.BandwidthValue, server.BandwidthUnit = float64(v.LegacyBandwidthMbps), asset.UnitMbps
		}
		if server.BillingMonths <= 0 {
			server.BillingMonths = asset.DefaultBillingMonths
		}
		out = append(out, server)
	}
	return out, nil
}

// disks 优先读取 disks_json,没有时退回早期版本的两块硬盘字段。
func (v VPS) disks() []asset.Disk {
	if disks := asset.ParseDisksJSON(v.DisksJSON); len(disks) > 0 {
		return disks
	}
	return asset.CleanDisks([]asset.Disk{
		{SizeGB: v.LegacyDiskGB, Type: v.LegacyDiskType},
		{SizeGB: v.LegacyDisk2GB, Type: v.LegacyDisk2Type},
	})
}

func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// amount 把非法的数值(负数、NaN)归零。
func amount(f float64) float64 {
	if !money.NonNegative(f) {
		return 0
	}
	return f
}

// currency 规范化币种代码,不认识的币种按默认币种处理。
func currency(code string) string {
	if code = money.Normalize(code); money.IsSupported(code) {
		return code
	}
	return money.DefaultCurrency
}

// timestampOr 校验 RFC 3339 时间戳,无效时返回 fallback。
func timestampOr(raw, fallback string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return t.Format(time.RFC3339)
}
