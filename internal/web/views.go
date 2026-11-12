package web

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"mb/internal/asset"
	"mb/internal/fx"
	"mb/internal/money"
)

// 本文件把资产数据转换成模板直接使用的"视图"。
//
// 访客能看到什么在这里决定:不该展示的字段根本不会放进视图,
// 模板只负责排版,不承担任何权限判断,也就不会因为模板写错而泄露数据。

// expiryView 是到期时间的展示数据。
type expiryView struct {
	Date  string // 具体日期 YYYY-MM-DD;永久有效时为空
	Label string // 12 天 / 今天到期 / 过期 3 天 / 永久
	Tone  string // normal / warn / danger / expired / permanent / invalid,决定标签颜色
	ISO   string // 供前端排序
	Days  string // 供前端筛选;永久或日期无效时为空
}

// priceView 是续费价格的展示数据。
type priceView struct {
	Amount   string // "12.99 USD" 或 "免费"
	Base     string // 折合统计币种:"≈ ¥93.5";币种相同时为空
	Billing  string // 付费周期,仅非年付时显示:"月付"
	Annual   string // 年化价格,仅非年付时显示:"年化 ≈ ¥1,200"
	NoRate   bool   // 缺少汇率,无法折算
	Excluded bool   // 不计入总成本
	Sort     string // 折合统计币种的年化价格,供前端排序
}

type domainRow struct {
	ID        int64
	Name      string
	NameParts []string // 域名按 "." 拆开的各段:长域名优先在点后面换行
	URL       string
	Registrar string
	Expiry    expiryView
	Price     priceView
	Notes     string
	Pinned    bool
	Hidden    bool

	SortName string
	Suffix   string
	Search   string
}

type domainTable struct {
	ShowRegistrar bool
	ShowExpiry    bool
	ShowPrice     bool
	ShowNotes     bool
	Rows          []domainRow
	Concealed     int // 对访客隐藏的条数
}

type vpsRow struct {
	ID      int64
	Vendor  string
	IP      string
	Region  string
	Chips   []string // 2C/4G、80GB SSD、1TB 双向 ...
	Specs   string
	Expiry  expiryView
	Price   priceView
	Notes   string
	Pinned  bool
	Hidden  bool
	Billing int // 付费周期(月),续费对话框的默认值

	SortName string
	Search   string
}

type vpsTable struct {
	ShowIP     bool
	ShowRegion bool
	ShowSpecs  bool
	ShowExpiry bool
	ShowPrice  bool
	ShowNotes  bool
	Rows       []vpsRow
	Concealed  int
}

// viewBuilder 携带构建视图所需的上下文,并在构建过程中累计年度成本。
type viewBuilder struct {
	now   time.Time
	base  string // 统计币种
	rates money.Rates
	admin bool
	vis   visibility

	unconverted int            // 因缺少汇率而没能计入总成本的条数
	usage       map[string]int // 各币种被多少条记录使用
}

// expiry 生成到期标签。只有需要留意的(90 天内、已过期)才用醒目的颜色,
// 这样扫一眼列表就能看出哪些要处理。
func (b *viewBuilder) expiry(expires string) expiryView {
	if asset.IsPermanent(expires) {
		return expiryView{Label: "永久", Tone: "permanent", ISO: asset.Permanent}
	}
	days, ok := asset.DaysLeft(expires, b.now)
	if !ok {
		return expiryView{Date: expires, Label: "日期无效", Tone: "invalid", ISO: expires}
	}
	v := expiryView{Date: expires, ISO: expires, Days: strconv.Itoa(days)}
	switch {
	case days < 0:
		v.Label, v.Tone = fmt.Sprintf("过期 %d 天", -days), "expired"
	case days == 0:
		v.Label, v.Tone = "今天到期", "danger"
	case days <= 30:
		v.Label, v.Tone = fmt.Sprintf("%d 天", days), "danger"
	case days <= 90:
		v.Label, v.Tone = fmt.Sprintf("%d 天", days), "warn"
	default:
		v.Label, v.Tone = fmt.Sprintf("%d 天", days), "normal"
	}
	return v
}

// annualCost 返回一条记录折合统计币种的年度成本,同时累计币种使用情况。
func (b *viewBuilder) annualCost(price float64, currency string, months int, excluded bool) float64 {
	if price <= 0 {
		return 0
	}
	b.usage[currency]++
	if excluded {
		return 0
	}
	converted, ok := b.rates.Convert(asset.Annualize(price, months), currency, b.base)
	if !ok {
		b.unconverted++
		return 0
	}
	return converted
}

func (b *viewBuilder) price(price float64, currency string, months int, excluded bool) priceView {
	v := priceView{Excluded: excluded, Sort: "0"}
	if price <= 0 {
		v.Amount = "免费"
		return v
	}
	v.Amount = money.Format(price) + " " + currency
	if months != 12 {
		v.Billing = asset.BillingLabel(months)
	}
	converted, ok := b.rates.Convert(price, currency, b.base)
	if !ok {
		v.NoRate = true
		return v
	}
	if currency != b.base {
		v.Base = "≈ " + money.WithSymbol(b.base, converted)
	}
	annual := asset.Annualize(converted, months)
	if months != 12 {
		v.Annual = "年化 ≈ " + money.WithSymbol(b.base, annual)
	}
	v.Sort = strconv.FormatFloat(annual, 'f', 4, 64)
	return v
}

func (b *viewBuilder) domains(list []asset.Domain) (table domainTable, total float64) {
	table = domainTable{
		ShowRegistrar: b.vis.DomainRegistrar,
		ShowExpiry:    b.vis.DomainExpiry,
		ShowPrice:     b.vis.DomainPrice,
		ShowNotes:     b.vis.DomainNotes,
	}
	for _, d := range list {
		total += b.annualCost(d.Price, d.Currency, 12, d.ExcludeCost)

		if d.Hidden && !b.admin {
			table.Concealed++
			continue
		}
		row := domainRow{
			ID: d.ID, Name: d.Name, NameParts: strings.Split(d.Name, "."), URL: domainURL(d.Name),
			Pinned: d.Pinned, Hidden: d.Hidden,
			SortName: strings.ToLower(d.Name), Suffix: domainSuffix(d.Name),
		}
		search := []string{d.Name}
		if table.ShowRegistrar {
			row.Registrar = d.Registrar
			search = append(search, d.Registrar)
		}
		if table.ShowExpiry {
			row.Expiry = b.expiry(d.ExpiresAt)
		}
		if table.ShowPrice {
			row.Price = b.price(d.Price, d.Currency, 12, d.ExcludeCost)
		}
		if table.ShowNotes {
			row.Notes = d.Notes
			search = append(search, d.Notes)
		}
		row.Search = searchText(search)
		table.Rows = append(table.Rows, row)
	}
	return table, total
}

func (b *viewBuilder) servers(list []asset.VPS) (table vpsTable, total float64) {
	table = vpsTable{
		ShowIP:     b.vis.VPSIP,
		ShowRegion: b.vis.VPSRegion,
		ShowSpecs:  b.vis.VPSSpecs,
		ShowExpiry: b.vis.VPSExpiry,
		ShowPrice:  b.vis.VPSPrice,
		ShowNotes:  b.vis.VPSNotes,
	}
	for _, v := range list {
		total += b.annualCost(v.Price, v.Currency, v.BillingMonths, v.ExcludeCost)

		if v.Hidden && !b.admin {
			table.Concealed++
			continue
		}
		row := vpsRow{
			ID: v.ID, Vendor: v.Vendor, Pinned: v.Pinned, Hidden: v.Hidden,
			Billing: v.BillingMonths, SortName: strings.ToLower(v.Vendor),
		}
		search := []string{v.Vendor}
		if table.ShowIP {
			row.IP = v.IP
			search = append(search, v.IP)
		}
		if table.ShowRegion {
			row.Region = v.Region
			search = append(search, v.Region)
		}
		if table.ShowSpecs {
			row.Chips = specChips(v)
			row.Specs = v.Specs
			search = append(search, v.Specs)
		}
		if table.ShowExpiry {
			row.Expiry = b.expiry(v.ExpiresAt)
		}
		if table.ShowPrice {
			row.Price = b.price(v.Price, v.Currency, v.BillingMonths, v.ExcludeCost)
		}
		if table.ShowNotes {
			row.Notes = v.Notes
			search = append(search, v.Notes)
		}
		row.Search = searchText(search)
		table.Rows = append(table.Rows, row)
	}
	return table, total
}

// specChips 把结构化的配置渲染成一组短标签。
func specChips(v asset.VPS) []string {
	var chips []string
	switch {
	case v.Cores > 0 && v.MemoryGB > 0:
		chips = append(chips, fmt.Sprintf("%dC/%sG", v.Cores, money.Format(v.MemoryGB)))
	case v.Cores > 0:
		chips = append(chips, fmt.Sprintf("%dC", v.Cores))
	case v.MemoryGB > 0:
		chips = append(chips, money.Format(v.MemoryGB)+"G")
	}
	for _, d := range v.Disks {
		chips = append(chips, d.String())
	}
	if v.TrafficSet {
		traffic := "不限流量"
		if v.TrafficTB > 0 {
			traffic = money.Format(v.TrafficTB) + "TB"
		}
		switch v.TrafficDir {
		case asset.TrafficSingle:
			traffic += " 单向"
		case asset.TrafficDouble:
			traffic += " 双向"
		}
		chips = append(chips, traffic)
	}
	if v.BandwidthValue > 0 {
		chips = append(chips, money.Format(v.BandwidthValue)+v.BandwidthUnit)
	}
	return chips
}

// fxSummary 是面板上的汇率概览。
type fxSummary struct {
	Pairs    []string // "1 USD ≈ 7.21 CNY"
	Source   string
	RateDate string
	Error    string // 最近一次更新失败的原因,仅管理员可见
}

// maxFXPairs 是面板上最多展示的汇率条数。
const maxFXPairs = 4

// fxSummary 列出资产实际用到的币种对统计币种的汇率;没有外币资产时展示美元和欧元。
func (b *viewBuilder) fxSummary(state fx.State) fxSummary {
	codes := make([]string, 0, len(b.usage))
	for code := range b.usage {
		if code != b.base {
			codes = append(codes, code)
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		if b.usage[codes[i]] != b.usage[codes[j]] {
			return b.usage[codes[i]] > b.usage[codes[j]]
		}
		return codes[i] < codes[j]
	})
	if len(codes) == 0 {
		for _, code := range []string{"USD", "EUR", "CNY"} {
			if code != b.base && len(codes) < 2 {
				codes = append(codes, code)
			}
		}
	}

	summary := fxSummary{Source: sourceLabel(state), RateDate: state.RateDate}
	for _, code := range codes {
		if len(summary.Pairs) == maxFXPairs {
			break
		}
		if rate, ok := b.rates.Convert(1, code, b.base); ok {
			summary.Pairs = append(summary.Pairs, fmt.Sprintf("1 %s ≈ %s %s", code, money.FormatRate(rate), b.base))
		}
	}
	if b.admin {
		summary.Error = state.LastError
	}
	return summary
}

func sourceLabel(state fx.State) string {
	label := state.Source
	switch state.Source {
	case fx.SourceECB:
		label = "欧洲央行"
	case fx.SourceManual:
		label = "手动设置"
	case fx.SourceDefault:
		label = "内置默认值"
	}
	if len(state.Overrides) > 0 {
		label += "（含手动汇率）"
	}
	return label
}

// domainURL 返回点击域名时打开的地址。
func domainURL(name string) string {
	name = strings.TrimSpace(name)
	if u, err := url.Parse(name); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return u.String()
	}
	return (&url.URL{Scheme: "https", Host: name}).String()
}

// domainSuffix 返回域名的顶级后缀,供"按后缀排序"使用。
func domainSuffix(name string) string {
	name = strings.ToLower(strings.Trim(strings.TrimSpace(name), "."))
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func searchText(parts []string) string {
	return strings.ToLower(strings.Join(parts, " "))
}
