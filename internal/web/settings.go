package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mb/internal/backup"
	"mb/internal/mailer"
	"mb/internal/money"
	"mb/internal/reminder"
	"mb/internal/store"
	"mb/internal/usererr"
)

// 设置页由若干面板组成,每个面板是一个独立的表单,各自提交到自己的地址。
// 保存成功后重定向回对应面板并显示提示;校验失败时原地重新渲染,保留用户刚填的内容。

// 面板名称,同时也是页面上的锚点。
const (
	panelVisibility = "visibility"
	panelFX         = "fx"
	panelEmail      = "email"
	panelBackup     = "backup"
	panelSecurity   = "security"
)

// slowActionTimeout 是访问外部服务(SMTP、WebDAV、汇率源)的操作时限,需小于 HTTP 的写超时。
const slowActionTimeout = 45 * time.Second

var errInvalidFavicon = usererr.New("Favicon 链接无效：仅支持 https:// 开头的完整地址")

type settingsPage struct {
	base
	Panel string // 校验失败时要停留的面板;为空则由页面自行决定
	Error string

	Visibility      visibilityForm
	FX              fxPanel
	Favicon         string
	PasswordFromEnv bool // 密码由 MB_ADMIN_PASSWORD 管理,页面上不可修改
	Email           emailForm
	WebDAV          webdavForm
}

type visibilityForm struct {
	PublicMode bool
	Show       visibility
}

type fxPanel struct {
	AutoUpdate bool
	Source     string
	RateDate   string
	UpdatedAt  string
	LastError  string
	Samples    []string     // 几个常用币种的当前汇率
	Overrides  []fxOverride // 已有的手动汇率
	NewCode    string       // 以下两项是"新增手动汇率"一行的输入
	NewRate    string
	Currencies []money.Currency
}

type fxOverride struct {
	Code   string
	Name   string
	Rate   string
	Market string // 数据源给出的汇率,供对照
}

type emailForm struct {
	Enabled     bool
	Days        string
	Host        string
	Port        string
	Encryption  string
	Auth        string
	Username    string
	From        string
	To          string
	HasPassword bool // 已保存密码;密码本身不会回显到页面上
}

type webdavForm struct {
	URL         string
	Filename    string
	Username    string
	HasPassword bool
}

func (s *Server) settingsPage() *settingsPage {
	state := s.fx.State()
	mail := mailer.LoadSettings(s.store)
	remind := reminder.Load(s.store)
	dav := backup.LoadWebDAV(s.store)

	p := &settingsPage{
		base:            base{Title: "设置"},
		Visibility:      visibilityForm{PublicMode: s.publicMode(), Show: s.publicVisibility()},
		Favicon:         s.store.String(keyFaviconURL, ""),
		PasswordFromEnv: s.cfg.AdminPassword != "",
		FX: fxPanel{
			AutoUpdate: state.AutoUpdate,
			Source:     sourceLabel(state),
			RateDate:   state.RateDate,
			UpdatedAt:  state.UpdatedAt,
			LastError:  state.LastError,
		},
		Email: emailForm{
			Enabled: remind.Enabled, Days: reminder.FormatDays(remind.Days),
			Host: mail.Host, Port: mail.Port, Encryption: mail.Encryption, Auth: mail.Auth,
			Username: mail.Username, From: mail.From, To: mail.To,
			HasPassword: mail.Password != "",
		},
		WebDAV: webdavForm{
			URL: dav.URL, Filename: dav.Filename, Username: dav.Username,
			HasPassword: dav.Password != "",
		},
	}

	for _, code := range []string{"USD", "CNY", "GBP", "JPY"} {
		if rate, ok := state.Rates.Rate(code); ok {
			p.FX.Samples = append(p.FX.Samples, fmt.Sprintf("1 EUR = %s %s", money.FormatRate(rate), code))
		}
	}
	for code, rate := range state.Overrides {
		p.FX.Overrides = append(p.FX.Overrides, fxOverride{
			Code: code, Name: money.Name(code),
			Rate: strconv.FormatFloat(rate, 'f', -1, 64), Market: money.FormatRate(state.Market[code]),
		})
	}
	sort.Slice(p.FX.Overrides, func(i, j int) bool { return p.FX.Overrides[i].Code < p.FX.Overrides[j].Code })
	for _, c := range money.All() {
		if c.Code != "EUR" { // 汇率以欧元为基准,欧元自身恒为 1
			p.FX.Currencies = append(p.FX.Currencies, c)
		}
	}
	return p
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "settings", s.settingsPage())
}

// settingsSaved 跳回指定面板并显示成功提示。
func (s *Server) settingsSaved(w http.ResponseWriter, r *http.Request, panel, message string) {
	s.redirectWithFlash(w, r, "/admin/settings#"+panel, flashOK, message)
}

// settingsFailed 在指定面板上显示错误;patch 用来把用户刚提交的内容填回页面。
func (s *Server) settingsFailed(w http.ResponseWriter, r *http.Request, panel, message string, patch func(*settingsPage)) {
	p := s.settingsPage()
	p.Panel, p.Error = panel, message
	if patch != nil {
		patch(p)
	}
	s.render(w, r, http.StatusUnprocessableEntity, "settings", p)
}

// settingsError 处理保存设置时的错误:用户错误显示在面板上,内部错误只记日志。
func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, panel string, err error) {
	if !usererr.Is(err) {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	}
	s.settingsFailed(w, r, panel, usererr.Message(err, "操作失败，详情请查看服务端日志"), nil)
}

func checked(r *http.Request, name string) bool { return r.PostForm.Get(name) == "1" }

// ---- 展示(公开模式、访客可见范围、站点图标) ----

func (s *Server) handleSaveVisibility(w http.ResponseWriter, r *http.Request) {
	show := visibility{
		Totals:          checked(r, "show_totals"),
		DomainRegistrar: checked(r, "show_domain_registrar"),
		DomainExpiry:    checked(r, "show_domain_expiry"),
		DomainPrice:     checked(r, "show_domain_price"),
		DomainNotes:     checked(r, "show_domain_notes"),
		VPSIP:           checked(r, "show_vps_ip"),
		VPSRegion:       checked(r, "show_vps_region"),
		VPSSpecs:        checked(r, "show_vps_specs"),
		VPSExpiry:       checked(r, "show_vps_expiry"),
		VPSPrice:        checked(r, "show_vps_price"),
		VPSNotes:        checked(r, "show_vps_notes"),
	}
	publicMode := checked(r, "public_mode")
	favicon := strings.TrimSpace(r.PostForm.Get("favicon_url"))
	if err := validateFaviconURL(favicon); err != nil {
		s.settingsFailed(w, r, panelVisibility, err.Error(), func(p *settingsPage) {
			p.Visibility = visibilityForm{PublicMode: publicMode, Show: show}
			p.Favicon = favicon
		})
		return
	}

	values := show.settings()
	values[keyPublicMode] = store.BoolValue(publicMode)
	values[keyFaviconURL] = favicon
	if err := s.store.SetMany(r.Context(), values); err != nil {
		s.settingsError(w, r, panelVisibility, err)
		return
	}
	s.settingsSaved(w, r, panelVisibility, "展示设置已保存")
}

// ---- 汇率 ----

func (s *Server) handleSaveFX(w http.ResponseWriter, r *http.Request) {
	newCode := money.Normalize(r.PostForm.Get("new_code"))
	newRate := strings.TrimSpace(r.PostForm.Get("new_rate"))
	fail := func(message string) {
		s.settingsFailed(w, r, panelFX, message, func(p *settingsPage) {
			p.FX.NewCode, p.FX.NewRate = newCode, newRate
		})
	}

	// 已有的手动汇率:输入框留空表示删除这一条。
	overrides := money.Rates{}
	for code := range s.fx.State().Overrides {
		raw := strings.TrimSpace(r.PostForm.Get("rate_" + code))
		if raw == "" {
			continue
		}
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil || !money.Positive(rate) {
			fail(code + " 的手动汇率应为正数")
			return
		}
		overrides[code] = rate
	}
	if newCode != "" || newRate != "" {
		if newCode == "EUR" || !money.IsSupported(newCode) {
			fail("请选择要设置手动汇率的币种")
			return
		}
		rate, err := strconv.ParseFloat(newRate, 64)
		if err != nil || !money.Positive(rate) {
			fail("手动汇率应为正数")
			return
		}
		overrides[newCode] = rate
	}

	ctx := r.Context()
	if err := s.fx.SetOverrides(ctx, overrides); err != nil {
		s.settingsError(w, r, panelFX, err)
		return
	}
	if err := s.fx.SetAutoUpdate(ctx, checked(r, "auto_update")); err != nil {
		s.settingsError(w, r, panelFX, err)
		return
	}
	s.settingsSaved(w, r, panelFX, "汇率设置已保存")
}

func (s *Server) handleRefreshFX(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), slowActionTimeout)
	defer cancel()
	if err := s.fx.Refresh(ctx); err != nil {
		log.Printf("fx: manual refresh failed: %v", err)
		s.settingsFailed(w, r, panelFX, "汇率更新失败："+shorten(err.Error()), nil)
		return
	}
	s.settingsSaved(w, r, panelFX, "汇率已更新")
}

// ---- 修改密码 ----

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	fail := func(message string) { s.settingsFailed(w, r, panelSecurity, message, nil) }

	current := r.PostForm.Get("current_password")
	next := r.PostForm.Get("new_password")
	switch {
	case s.cfg.AdminPassword != "":
		fail("管理员密码由环境变量 MB_ADMIN_PASSWORD 管理，请修改环境变量后重启")
		return
	case utf8.RuneCountInString(next) < minPasswordLen:
		fail(fmt.Sprintf("新密码至少 %d 位", minPasswordLen))
		return
	case len(next) > maxPasswordLen:
		fail(fmt.Sprintf("新密码过长，最多 %d 个字节", maxPasswordLen))
		return
	case next != r.PostForm.Get("confirm_password"):
		fail("两次输入的新密码不一致")
		return
	case !s.checkPassword(current):
		fail("当前密码不正确")
		return
	}

	if err := setPassword(r.Context(), s.store, next); err != nil {
		s.settingsError(w, r, panelSecurity, err)
		return
	}
	// 签名密钥已更换,其它设备上的会话全部失效;给当前浏览器重新签发会话。
	s.startSession(w, r)
	s.settingsSaved(w, r, panelSecurity, "管理员密码已更新，其它设备需要重新登录")
}

// ---- 邮件提醒 ----

func (s *Server) handleSaveEmail(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	form := emailForm{
		Enabled:    checked(r, "enabled"),
		Days:       strings.TrimSpace(f.Get("days")),
		Host:       strings.TrimSpace(f.Get("host")),
		Port:       strings.TrimSpace(f.Get("port")),
		Encryption: f.Get("encryption"),
		Auth:       f.Get("auth"),
		Username:   strings.TrimSpace(f.Get("username")),
		From:       strings.TrimSpace(f.Get("from")),
		To:         strings.TrimSpace(f.Get("to")),
	}
	saved := mailer.LoadSettings(s.store)
	password := keepSecret(f.Get("password"), saved.Password, checked(r, "clear_password"))
	form.HasPassword = password != ""

	fail := func(err error) {
		s.settingsFailed(w, r, panelEmail, usererr.Message(err, "操作失败，详情请查看服务端日志"),
			func(p *settingsPage) { p.Email = form })
	}

	days, err := reminder.ParseDays(form.Days)
	if err != nil {
		fail(err)
		return
	}
	settings, err := mailer.Settings{
		Host: form.Host, Port: form.Port, Encryption: form.Encryption, Auth: form.Auth,
		Username: form.Username, Password: password, From: form.From, To: form.To,
	}.Normalize()
	if err != nil {
		fail(err)
		return
	}
	testing := f.Get("action") == "test"
	cfg, cfgErr := settings.Config()
	if cfgErr != nil && (testing || form.Enabled) {
		// 只是保存一份未启用的草稿时,允许配置不完整。
		fail(cfgErr)
		return
	}

	ctx := r.Context()
	if err := mailer.SaveSettings(ctx, s.store, settings); err != nil {
		s.settingsError(w, r, panelEmail, err)
		return
	}
	if err := reminder.Save(ctx, s.store, reminder.Settings{Enabled: form.Enabled, Days: days}); err != nil {
		s.settingsError(w, r, panelEmail, err)
		return
	}
	if !testing {
		s.settingsSaved(w, r, panelEmail, "邮件提醒设置已保存")
		return
	}

	sendCtx, cancel := context.WithTimeout(ctx, slowActionTimeout)
	defer cancel()
	err = s.sendMail(sendCtx, cfg, mailer.Message{
		Subject: "MB 资产面板邮件测试",
		Body:    "这是一封测试邮件，用于验证到期提醒的邮件配置是否可用。\n\n—— MB 资产面板\n",
	})
	if err != nil {
		log.Printf("email test failed: %v", err)
		s.settingsFailed(w, r, panelEmail, "设置已保存，但测试邮件发送失败："+shorten(err.Error()), nil)
		return
	}
	s.settingsSaved(w, r, panelEmail, "设置已保存，测试邮件已发送")
}

// keepSecret 决定密码类字段的新值:页面不回显已保存的密码,所以留空表示保持不变,
// 只有勾选"清除"才会真的清空。
func keepSecret(submitted, saved string, clear bool) string {
	switch {
	case clear:
		return ""
	case submitted == "":
		return saved
	}
	return submitted
}

// shorten 截断外部服务返回的错误信息,避免提示条过长。
func shorten(message string) string {
	const limit = 240
	if runes := []rune(message); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return message
}
