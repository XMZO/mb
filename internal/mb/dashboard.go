package mb

import (
	"net/http"
	"time"
)

func (a *app) handleDashboard(w http.ResponseWriter, r *http.Request) {
	isAdmin := a.isAdmin(r)
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	base := baseCurrencyFromRequest(r)
	favicon := a.getFaviconURL()

	fx := a.getFxInfo()
	if fx.AutoUpdate {
		a.maybeAutoUpdateFx(fx)
	}

	domains, domainTotalBase, err := a.loadDomains(r.Context(), fx, base)
	if err != nil {
		a.serverError(w, err)
		return
	}
	vpsRows, vpsTotalBase, err := a.loadVPS(r.Context(), fx, base)
	if err != nil {
		a.serverError(w, err)
		return
	}

	domain30, domain90 := 0, 0
	for _, d := range domains {
		if d.IsPermanent {
			continue
		}
		if d.DaysLeft >= 0 && d.DaysLeft < 30 {
			domain30++
		}
		if d.DaysLeft >= 0 && d.DaysLeft < 90 {
			domain90++
		}
	}
	vps30, vps90 := 0, 0
	for _, v := range vpsRows {
		if v.IsPermanent {
			continue
		}
		if v.DaysLeft >= 0 && v.DaysLeft < 30 {
			vps30++
		}
		if v.DaysLeft >= 0 && v.DaysLeft < 90 {
			vps90++
		}
	}

	showTotals := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowTotals, true)
	domainShowRegistrar := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowDomainRegistrar, true)
	domainShowExpiry := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowDomainExpiry, true)
	domainShowPrice := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowDomainPrice, true)
	domainShowNotes := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowDomainNotes, true)
	vpsShowIP := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSIP, true)
	vpsShowRegion := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSRegion, true)
	vpsShowSpecs := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSSpecs, true)
	vpsShowExpiry := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSExpiry, true)
	vpsShowPrice := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSPrice, true)
	vpsShowNotes := isAdmin || !publicMode || mustGetBoolSetting(a.db, settingPublicShowVPSNotes, true)

	vm := dashboardVM{
		NowISO:              time.Now().In(time.Local).Format(time.RFC3339),
		IsAdmin:             isAdmin,
		PublicMode:          publicMode,
		FaviconURL:          favicon,
		Fx:                  fx,
		BaseCurrency:        base,
		BaseSymbol:          currencySymbol(base),
		ShowTotals:          showTotals,
		DomainShowRegistrar: domainShowRegistrar,
		DomainShowExpiry:    domainShowExpiry,
		DomainShowPrice:     domainShowPrice,
		DomainShowNotes:     domainShowNotes,
		VPSShowIP:           vpsShowIP,
		VPSShowRegion:       vpsShowRegion,
		VPSShowSpecs:        vpsShowSpecs,
		VPSShowExpiry:       vpsShowExpiry,
		VPSShowPrice:        vpsShowPrice,
		VPSShowNotes:        vpsShowNotes,
		Domains:             domains,
		VPS:                 vpsRows,
		DomainTotalBase:     domainTotalBase,
		VPSTotalBase:        vpsTotalBase,
		GrandTotalBase:      domainTotalBase + vpsTotalBase,
		DomainExpiring30:    domain30,
		DomainExpiring90:    domain90,
		VPSExpiring30:       vps30,
		VPSExpiring90:       vps90,
		AllExpiring30Total:  domain30 + vps30,
	}
	a.render(w, "dashboard", vm)
}
