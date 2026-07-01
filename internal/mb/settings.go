package mb

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (a *app) getFaviconURL() string {
	v, ok, err := getSetting(a.db, settingFaviconURL)
	if err != nil || !ok {
		return ""
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if err := validateFaviconURL(v); err != nil {
		return ""
	}
	return v
}
func validateFaviconURL(v string) error {
	if len(v) > 2048 {
		return errors.New("favicon url too long")
	}
	u, err := url.Parse(v)
	if err != nil {
		return err
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return errors.New("favicon url must be https")
	}
	if u.Host == "" {
		return errors.New("favicon url missing host")
	}
	return nil
}
func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	if r.Method == http.MethodGet {
		notice := settingsNoticeFromQuery(r)
		a.render(w, "settings", a.buildSettingsVM(publicMode, "", notice))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch r.FormValue("form") {
	case "access":
		a.handleSettingsAccess(w, r)
	case "password":
		a.handleSettingsPassword(w, r)
	case "email":
		a.handleSettingsEmail(w, r)
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
	}
}
func settingsNoticeFromQuery(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("notice"))) {
	case "email_test_sent":
		return "测试邮件已发送"
	default:
		return ""
	}
}
func (a *app) handleSettingsAccess(w http.ResponseWriter, r *http.Request) {
	newPublicMode := r.FormValue("public_mode") == "1"
	newFxAuto := r.FormValue("fx_auto_update") == "1"
	publicShowTotals := r.FormValue("public_show_totals") == "1"
	publicShowDomainRegistrar := r.FormValue("public_show_domain_registrar") == "1"
	publicShowDomainExpiry := r.FormValue("public_show_domain_expiry") == "1"
	publicShowDomainPrice := r.FormValue("public_show_domain_price") == "1"
	publicShowDomainNotes := r.FormValue("public_show_domain_notes") == "1"
	publicShowVPSIP := r.FormValue("public_show_vps_ip") == "1"
	publicShowVPSRegion := r.FormValue("public_show_vps_region") == "1"
	publicShowVPSSpecs := r.FormValue("public_show_vps_specs") == "1"
	publicShowVPSExpiry := r.FormValue("public_show_vps_expiry") == "1"
	publicShowVPSPrice := r.FormValue("public_show_vps_price") == "1"
	publicShowVPSNotes := r.FormValue("public_show_vps_notes") == "1"
	faviconURL := strings.TrimSpace(r.FormValue("favicon_url"))

	renderAccessError := func(message string) {
		vm := a.buildSettingsVM(newPublicMode, message, "")
		vm.PublicShowTotals = publicShowTotals
		vm.PublicShowDomainRegistrar = publicShowDomainRegistrar
		vm.PublicShowDomainExpiry = publicShowDomainExpiry
		vm.PublicShowDomainPrice = publicShowDomainPrice
		vm.PublicShowDomainNotes = publicShowDomainNotes
		vm.PublicShowVPSIP = publicShowVPSIP
		vm.PublicShowVPSRegion = publicShowVPSRegion
		vm.PublicShowVPSSpecs = publicShowVPSSpecs
		vm.PublicShowVPSExpiry = publicShowVPSExpiry
		vm.PublicShowVPSPrice = publicShowVPSPrice
		vm.PublicShowVPSNotes = publicShowVPSNotes
		vm.FaviconURL = faviconURL
		a.render(w, "settings", vm)
	}

	if faviconURL != "" {
		if err := validateFaviconURL(faviconURL); err != nil {
			renderAccessError("Favicon 链接无效（仅支持 https 完整链接）")
			return
		}
	}

	if err := setSetting(a.db, settingPublicMode, boolTo01(newPublicMode)); err != nil {
		a.serverError(w, err)
		return
	}
	if err := setSetting(a.db, settingFxAutoUpdate, boolTo01(newFxAuto)); err != nil {
		a.serverError(w, err)
		return
	}
	publicVisibilitySettings := []struct {
		key   string
		value bool
	}{
		{settingPublicShowTotals, publicShowTotals},
		{settingPublicShowDomainRegistrar, publicShowDomainRegistrar},
		{settingPublicShowDomainExpiry, publicShowDomainExpiry},
		{settingPublicShowDomainPrice, publicShowDomainPrice},
		{settingPublicShowDomainNotes, publicShowDomainNotes},
		{settingPublicShowVPSIP, publicShowVPSIP},
		{settingPublicShowVPSRegion, publicShowVPSRegion},
		{settingPublicShowVPSSpecs, publicShowVPSSpecs},
		{settingPublicShowVPSExpiry, publicShowVPSExpiry},
		{settingPublicShowVPSPrice, publicShowVPSPrice},
		{settingPublicShowVPSNotes, publicShowVPSNotes},
	}
	for _, item := range publicVisibilitySettings {
		if err := setSetting(a.db, item.key, boolTo01(item.value)); err != nil {
			a.serverError(w, err)
			return
		}
	}
	if err := setSetting(a.db, settingFaviconURL, faviconURL); err != nil {
		a.serverError(w, err)
		return
	}

	notice := "已保存设置"
	manualUpdated := false
	errInvalidRate := errors.New("invalid rate")
	rates := a.getFxRates()
	parseManualRate := func(raw string) (float64, bool, error) {
		if raw == "" {
			return 0, false, nil
		}
		val, err := strconv.ParseFloat(raw, 64)
		if err != nil || !isFinitePositive(val) {
			return 0, false, errInvalidRate
		}
		return val, true, nil
	}
	for _, currency := range manualFxCurrencies() {
		rate, changed, err := parseManualRate(strings.TrimSpace(r.FormValue("fx_rate_" + currency.Code)))
		if err != nil {
			if !errors.Is(err, errInvalidRate) {
				a.serverError(w, err)
				return
			}
			renderAccessError("手动汇率格式不正确（需要正数）")
			return
		}
		if changed {
			rates[currency.Code] = rate
			manualUpdated = true
		}
	}
	if manualUpdated {
		if err := storeFxRates(a.db, rates, time.Now().In(time.Local).Format("2006-01-02"), fxSourceManual); err != nil {
			a.serverError(w, err)
			return
		}
		notice = "已保存手动汇率"
	}

	vm := a.buildSettingsVM(newPublicMode, "", notice)
	vm.FaviconURL = faviconURL
	a.render(w, "settings", vm)
}
func (a *app) handleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()
	current := r.FormValue("current_password")
	newPass := r.FormValue("new_password")
	newPass2 := r.FormValue("new_password2")

	if strings.TrimSpace(newPass) == "" {
		vm := a.buildSettingsVM(publicMode, "新密码不能为空", "")
		vm.FaviconURL = favicon
		a.render(w, "settings", vm)
		return
	}
	if newPass != newPass2 {
		vm := a.buildSettingsVM(publicMode, "两次输入的新密码不一致", "")
		vm.FaviconURL = favicon
		a.render(w, "settings", vm)
		return
	}
	if len(newPass) < 8 {
		vm := a.buildSettingsVM(publicMode, "新密码至少 8 位", "")
		vm.FaviconURL = favicon
		a.render(w, "settings", vm)
		return
	}

	hash, ok, err := getSetting(a.db, settingPasswordHash)
	if err != nil || !ok {
		a.serverError(w, errors.New("server not initialized"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		vm := a.buildSettingsVM(publicMode, "当前密码不正确", "")
		vm.FaviconURL = favicon
		a.render(w, "settings", vm)
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if err := setSetting(a.db, settingPasswordHash, string(newHash)); err != nil {
		a.serverError(w, err)
		return
	}

	vm := a.buildSettingsVM(publicMode, "", "管理员密码已更新")
	vm.FaviconURL = favicon
	a.render(w, "settings", vm)
}
func (a *app) handleSettingsEmail(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	form, errMsg := parseEmailSettingsForm(r)
	if errMsg != "" {
		vm := a.buildSettingsVM(publicMode, errMsg, "")
		applyEmailFormToVM(&vm, form)
		a.render(w, "settings", vm)
		return
	}
	if err := persistEmailSettings(a.db, form); err != nil {
		a.serverError(w, err)
		return
	}
	vm := a.buildSettingsVM(publicMode, "", "邮件提醒设置已保存")
	applyEmailFormToVM(&vm, form)
	a.render(w, "settings", vm)
}
func (a *app) handleEmailTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	form, errMsg := parseEmailSettingsForm(r)
	if errMsg != "" {
		vm := a.buildSettingsVM(publicMode, errMsg, "")
		applyEmailFormToVM(&vm, form)
		a.render(w, "settings", vm)
		return
	}
	if err := persistEmailSettings(a.db, form); err != nil {
		a.serverError(w, err)
		return
	}

	cfg, err := buildEmailConfig(form)
	if err != nil {
		log.Printf("settings error: test email config: %v", err)
		vm := a.buildSettingsVM(publicMode, "测试发送失败,请检查邮件配置或查看服务端日志", "")
		applyEmailFormToVM(&vm, form)
		a.render(w, "settings", vm)
		return
	}
	subject := "MB 资产面板邮件测试"
	body := "这是一封测试邮件，用于验证到期提醒配置是否可用。\n\n来自：MB 资产面板"
	if err := sendEmail(cfg, subject, body); err != nil {
		log.Printf("settings error: test email send: %v", err)
		vm := a.buildSettingsVM(publicMode, "测试发送失败,请检查邮件配置或查看服务端日志", "")
		applyEmailFormToVM(&vm, form)
		a.render(w, "settings", vm)
		return
	}

	http.Redirect(w, r, "/admin/settings?notice=email_test_sent", http.StatusSeeOther)
}
func (a *app) renderSettingsMessage(w http.ResponseWriter, errMsg, notice string) {
	a.render(w, "settings", a.buildSettingsVM(mustGetBoolSetting(a.db, settingPublicMode, false), errMsg, notice))
}
func (a *app) buildSettingsVM(publicMode bool, errMsg, notice string) settingsVM {
	return settingsVM{
		IsAdmin:                   true,
		PublicMode:                publicMode,
		FaviconURL:                a.getFaviconURL(),
		Fx:                        a.getFxInfo(),
		PublicShowTotals:          mustGetBoolSetting(a.db, settingPublicShowTotals, true),
		PublicShowDomainRegistrar: mustGetBoolSetting(a.db, settingPublicShowDomainRegistrar, true),
		PublicShowDomainExpiry:    mustGetBoolSetting(a.db, settingPublicShowDomainExpiry, true),
		PublicShowDomainPrice:     mustGetBoolSetting(a.db, settingPublicShowDomainPrice, true),
		PublicShowDomainNotes:     mustGetBoolSetting(a.db, settingPublicShowDomainNotes, true),
		PublicShowVPSIP:           mustGetBoolSetting(a.db, settingPublicShowVPSIP, true),
		PublicShowVPSRegion:       mustGetBoolSetting(a.db, settingPublicShowVPSRegion, true),
		PublicShowVPSSpecs:        mustGetBoolSetting(a.db, settingPublicShowVPSSpecs, true),
		PublicShowVPSExpiry:       mustGetBoolSetting(a.db, settingPublicShowVPSExpiry, true),
		PublicShowVPSPrice:        mustGetBoolSetting(a.db, settingPublicShowVPSPrice, true),
		PublicShowVPSNotes:        mustGetBoolSetting(a.db, settingPublicShowVPSNotes, true),
		WebDAVURL:                 mustGetStringSetting(a.db, settingWebDAVURL, ""),
		WebDAVFile:                mustGetStringSetting(a.db, settingWebDAVFile, ""),
		WebDAVUser:                mustGetStringSetting(a.db, settingWebDAVUser, ""),
		WebDAVPass:                mustGetStringSetting(a.db, settingWebDAVPass, ""),
		EmailEnabled:              mustGetBoolSetting(a.db, settingEmailEnabled, false),
		EmailDays:                 mustGetStringSetting(a.db, settingEmailDays, defaultEmailDaysList),
		EmailProtocol:             mustGetStringSetting(a.db, settingEmailProtocol, ""),
		EmailHost:                 mustGetStringSetting(a.db, settingEmailHost, ""),
		EmailPort:                 mustGetStringSetting(a.db, settingEmailPort, ""),
		EmailEncryption:           mustGetStringSetting(a.db, settingEmailEncrypt, "auto"),
		EmailAuth:                 mustGetStringSetting(a.db, settingEmailAuth, "plain"),
		EmailUser:                 mustGetStringSetting(a.db, settingEmailUser, ""),
		EmailPass:                 mustGetStringSetting(a.db, settingEmailPass, ""),
		EmailFrom:                 mustGetStringSetting(a.db, settingEmailFrom, ""),
		EmailTo:                   mustGetStringSetting(a.db, settingEmailTo, ""),
		Error:                     errMsg,
		Notice:                    notice,
	}
}
func (a *app) persistWebDAVSettings(rawURL, filename, user, pass string) {
	_ = setSetting(a.db, settingWebDAVURL, strings.TrimSpace(rawURL))
	_ = setSetting(a.db, settingWebDAVFile, strings.TrimSpace(filename))
	_ = setSetting(a.db, settingWebDAVUser, strings.TrimSpace(user))
	_ = setSetting(a.db, settingWebDAVPass, strings.TrimSpace(pass))
}
