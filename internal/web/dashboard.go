package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"mb/internal/money"
	"mb/internal/store"
)

// 与公开展示相关的设置项 key。
const (
	keyPublicMode = "public_mode"
	keyFaviconURL = "favicon_url"

	keyShowTotals          = "public_show_totals"
	keyShowDomainRegistrar = "public_show_domain_registrar"
	keyShowDomainExpiry    = "public_show_domain_expiry"
	keyShowDomainPrice     = "public_show_domain_price"
	keyShowDomainNotes     = "public_show_domain_notes"
	keyShowVPSIP           = "public_show_vps_ip"
	keyShowVPSRegion       = "public_show_vps_region"
	keyShowVPSSpecs        = "public_show_vps_specs"
	keyShowVPSExpiry       = "public_show_vps_expiry"
	keyShowVPSPrice        = "public_show_vps_price"
	keyShowVPSNotes        = "public_show_vps_notes"
)

// visibility 描述访客在公开模式下能看到哪些内容。
type visibility struct {
	Totals bool // 年度续费成本

	DomainRegistrar bool
	DomainExpiry    bool
	DomainPrice     bool
	DomainNotes     bool

	VPSIP     bool
	VPSRegion bool
	VPSSpecs  bool
	VPSExpiry bool
	VPSPrice  bool
	VPSNotes  bool
}

// allVisible 是管理员看到的内容:一切。
var allVisible = visibility{true, true, true, true, true, true, true, true, true, true, true}

// publicVisibility 读取为访客配置的可见范围;没有配置过的项默认可见。
func (s *Server) publicVisibility() visibility {
	show := func(key string) bool { return s.store.Bool(key, true) }
	return visibility{
		Totals:          show(keyShowTotals),
		DomainRegistrar: show(keyShowDomainRegistrar),
		DomainExpiry:    show(keyShowDomainExpiry),
		DomainPrice:     show(keyShowDomainPrice),
		DomainNotes:     show(keyShowDomainNotes),
		VPSIP:           show(keyShowVPSIP),
		VPSRegion:       show(keyShowVPSRegion),
		VPSSpecs:        show(keyShowVPSSpecs),
		VPSExpiry:       show(keyShowVPSExpiry),
		VPSPrice:        show(keyShowVPSPrice),
		VPSNotes:        show(keyShowVPSNotes),
	}
}

func (v visibility) settings() map[string]string {
	return map[string]string{
		keyShowTotals:          store.BoolValue(v.Totals),
		keyShowDomainRegistrar: store.BoolValue(v.DomainRegistrar),
		keyShowDomainExpiry:    store.BoolValue(v.DomainExpiry),
		keyShowDomainPrice:     store.BoolValue(v.DomainPrice),
		keyShowDomainNotes:     store.BoolValue(v.DomainNotes),
		keyShowVPSIP:           store.BoolValue(v.VPSIP),
		keyShowVPSRegion:       store.BoolValue(v.VPSRegion),
		keyShowVPSSpecs:        store.BoolValue(v.VPSSpecs),
		keyShowVPSExpiry:       store.BoolValue(v.VPSExpiry),
		keyShowVPSPrice:        store.BoolValue(v.VPSPrice),
		keyShowVPSNotes:        store.BoolValue(v.VPSNotes),
	}
}

func (s *Server) publicMode() bool { return s.store.Bool(keyPublicMode, false) }

// faviconURL 返回用户自定义的站点图标地址;未设置或不合法时为空。
func (s *Server) faviconURL() string {
	raw := s.store.String(keyFaviconURL, "")
	if validateFaviconURL(raw) != nil {
		return ""
	}
	return raw
}

type dashboardPage struct {
	base
	BaseCurrency string
	Currencies   []money.Currency
	Stats        dashboardStats
	FX           fxSummary
	Domains      domainTable
	VPS          vpsTable
}

type dashboardStats struct {
	DomainCount int
	VPSCount    int

	// 以下各项只在 ShowTotals 为 true 时填充。
	ShowTotals  bool
	Total       string // 折合统计币种的年度续费成本
	DomainTotal string
	VPSTotal    string
	Unconverted int // 因缺少汇率而没有计入的条数
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// 切换统计币种:记到 Cookie 里,再跳回干净的地址。
	if code := r.URL.Query().Get("base"); code != "" {
		s.setBaseCurrency(w, r, code)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	domains, err := s.store.Domains(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	servers, err := s.store.VPSList(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	admin := s.isAdmin(r)
	state := s.fx.State()
	builder := &viewBuilder{
		now:   time.Now(),
		base:  s.baseCurrency(r),
		rates: state.Rates,
		admin: admin,
		vis:   s.publicVisibility(),
		usage: make(map[string]int),
	}
	if admin {
		builder.vis = allVisible
	}

	domainTable, domainTotal := builder.domains(domains)
	vpsTable, vpsTotal := builder.servers(servers)

	stats := dashboardStats{DomainCount: len(domains), VPSCount: len(servers)}
	if builder.vis.Totals {
		stats.ShowTotals = true
		stats.Total = money.WithSymbol(builder.base, domainTotal+vpsTotal)
		stats.DomainTotal = money.WithSymbol(builder.base, domainTotal)
		stats.VPSTotal = money.WithSymbol(builder.base, vpsTotal)
		stats.Unconverted = builder.unconverted
	}

	s.render(w, r, http.StatusOK, "dashboard", &dashboardPage{
		base:         base{Title: "资产面板"},
		BaseCurrency: builder.base,
		Currencies:   money.All(),
		Stats:        stats,
		FX:           builder.fxSummary(state),
		Domains:      domainTable,
		VPS:          vpsTable,
	})
}

// ---- 统计币种 ----

func (s *Server) baseCurrencyCookie() string { return s.cfg.CookieName + "_base" }

// baseCurrency 返回访问者选择的统计币种,默认人民币。
func (s *Server) baseCurrency(r *http.Request) string {
	if c, err := r.Cookie(s.baseCurrencyCookie()); err == nil {
		if code := money.Normalize(c.Value); money.IsSupported(code) {
			return code
		}
	}
	return money.DefaultCurrency
}

func (s *Server) setBaseCurrency(w http.ResponseWriter, r *http.Request, code string) {
	code = money.Normalize(code)
	if !money.IsSupported(code) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.baseCurrencyCookie(),
		Value:    code,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
}

// validateFaviconURL 只接受 https 的完整地址(页面的 CSP 也只放行 https 图片)。空值表示不设置。
func validateFaviconURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return errInvalidFavicon
	}
	return nil
}
