// Package asset 定义域名与 VPS 两类资产的数据模型,以及与存储、界面无关的纯计算。
package asset

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind 区分资产类型。
type Kind string

const (
	KindDomain Kind = "domain"
	KindVPS    Kind = "vps"
)

// ParseKind 解析 URL / 表单中的资产类型。
func ParseKind(s string) (Kind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "domain", "domains":
		return KindDomain, true
	case "vps":
		return KindVPS, true
	}
	return "", false
}

// Label 返回资产类型的中文名称。
func (k Kind) Label() string {
	if k == KindDomain {
		return "域名"
	}
	return "VPS"
}

// Domain 是一条域名记录。Price 为每年的续费价格。
type Domain struct {
	ID          int64
	Name        string
	Registrar   string
	ExpiresAt   string // YYYY-MM-DD,永久为 Permanent
	Price       float64
	Currency    string
	Notes       string
	ExcludeCost bool // 不计入总成本
	Pinned      bool
	Hidden      bool // 公开模式下对访客隐藏
	SortOrder   int
	CreatedAt   string
	UpdatedAt   string
}

// Disk 是 VPS 的一块硬盘。Type 为 SSD、HDD 或空(不标注)。
type Disk struct {
	SizeGB int    `json:"size_gb"`
	Type   string `json:"type"`
}

// String 返回形如 "80GB SSD" 的展示文本。
func (d Disk) String() string {
	if d.Type == "" {
		return strconv.Itoa(d.SizeGB) + "GB"
	}
	return strconv.Itoa(d.SizeGB) + "GB " + d.Type
}

// VPS 是一条服务器记录。Price 为每个付费周期(BillingMonths 个月)的续费价格。
type VPS struct {
	ID     int64
	Vendor string
	IP     string
	Region string
	Specs  string // 自由填写的套餐描述

	Cores    int
	MemoryGB float64
	Disks    []Disk

	TrafficSet bool    // false 表示未填写流量
	TrafficTB  float64 // TrafficSet 且为 0 表示不限流量
	TrafficDir string  // TrafficSingle / TrafficDouble / 空

	BandwidthValue float64
	BandwidthUnit  string // UnitMbps / UnitGbps

	ExpiresAt     string
	Price         float64
	Currency      string
	BillingMonths int
	Notes         string
	ExcludeCost   bool
	Pinned        bool
	Hidden        bool
	SortOrder     int
	CreatedAt     string
	UpdatedAt     string
}

const (
	DefaultBillingMonths = 12
	MaxDisks             = 16

	TrafficSingle = "single"
	TrafficDouble = "double"

	UnitMbps = "Mbps"
	UnitGbps = "Gbps"
)

// AnnualPrice 把按周期计的续费价格折算为每年的价格。
func (v VPS) AnnualPrice() float64 { return Annualize(v.Price, v.BillingMonths) }

// Annualize 把 months 个月一付的价格折算为年价。
func Annualize(price float64, months int) float64 {
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || months <= 0 {
		return 0
	}
	return price * 12 / float64(months)
}

// BillingLabel 返回付费周期的中文描述。
func BillingLabel(months int) string {
	switch months {
	case 1:
		return "月付"
	case 3:
		return "季付"
	case 6:
		return "半年付"
	case 12:
		return "年付"
	case 24:
		return "两年付"
	case 36:
		return "三年付"
	}
	if months > 0 {
		return fmt.Sprintf("%d 个月", months)
	}
	return ""
}

// NormalizeDiskType 只接受 SSD / HDD,其余返回空。
func NormalizeDiskType(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "SSD":
		return "SSD"
	case "HDD":
		return "HDD"
	}
	return ""
}

// NormalizeTrafficDir 接受 single/double 及其中文写法,其余返回空。
func NormalizeTrafficDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case TrafficSingle, "单向":
		return TrafficSingle
	case TrafficDouble, "双向":
		return TrafficDouble
	}
	return ""
}

// NormalizeBandwidthUnit 返回 Gbps 或默认的 Mbps。
func NormalizeBandwidthUnit(unit string) string {
	if strings.EqualFold(strings.TrimSpace(unit), UnitGbps) {
		return UnitGbps
	}
	return UnitMbps
}

// CleanDisks 丢弃容量非正的条目并规范化类型。
func CleanDisks(disks []Disk) []Disk {
	var out []Disk
	for _, d := range disks {
		if d.SizeGB <= 0 {
			continue
		}
		out = append(out, Disk{SizeGB: d.SizeGB, Type: NormalizeDiskType(d.Type)})
	}
	return out
}

// ParseDisksJSON 解析存储中的硬盘列表;内容为空或损坏时返回 nil。
func ParseDisksJSON(raw string) []Disk {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var disks []Disk
	if err := json.Unmarshal([]byte(raw), &disks); err != nil {
		return nil
	}
	return CleanDisks(disks)
}

// DisksJSON 把硬盘列表序列化为存储格式;没有硬盘时返回空字符串。
func DisksJSON(disks []Disk) string {
	disks = CleanDisks(disks)
	if len(disks) == 0 {
		return ""
	}
	b, err := json.Marshal(disks)
	if err != nil {
		return ""
	}
	return string(b)
}
