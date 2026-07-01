package mb

import "net/http"

// featureModule 是功能模块的路由注册点。新增/删除功能时只需要增删一个模块,
// handler 与模板仍可独立替换,不会把所有路由写死在 Run 里。
type featureModule struct {
	name     string
	register func(*http.ServeMux, *app)
}

func defaultModules() []featureModule {
	return []featureModule{
		{name: "dashboard", register: registerDashboardRoutes},
		{name: "auth", register: registerAuthRoutes},
		{name: "domains", register: registerDomainRoutes},
		{name: "vps", register: registerVPSRoutes},
		{name: "settings", register: registerSettingsRoutes},
		{name: "sorting", register: registerSortRoutes},
		{name: "backup", register: registerBackupRoutes},
	}
}

func (a *app) registerRoutes(mux *http.ServeMux) {
	for _, m := range defaultModules() {
		m.register(mux, a)
	}
}

func registerDashboardRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/", a.withView(a.handleDashboard))
}

func registerAuthRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/logout", a.withAdmin(a.handleLogout))
}

func registerDomainRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/domain/new", a.withAdmin(a.handleDomainNew))
	mux.HandleFunc("/domain/edit", a.withAdmin(a.handleDomainEdit))
	mux.HandleFunc("/domain/delete", a.withAdmin(a.handleDomainDelete))
	mux.HandleFunc("/domain/renew", a.withAdmin(a.handleDomainRenew))
	mux.HandleFunc("/domain/pin", a.withAdmin(a.handleDomainPin))
	mux.HandleFunc("/domain/batch", a.withAdmin(a.handleDomainBatch))
	mux.HandleFunc("/domain/export", a.withAdmin(a.handleDomainExport))
}

func registerVPSRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/vps/new", a.withAdmin(a.handleVPSNew))
	mux.HandleFunc("/vps/edit", a.withAdmin(a.handleVPSEdit))
	mux.HandleFunc("/vps/delete", a.withAdmin(a.handleVPSDelete))
	mux.HandleFunc("/vps/renew", a.withAdmin(a.handleVPSRenew))
	mux.HandleFunc("/vps/pin", a.withAdmin(a.handleVPSPin))
	mux.HandleFunc("/vps/batch", a.withAdmin(a.handleVPSBatch))
}

func registerSettingsRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/admin/settings", a.withAdmin(a.handleSettings))
	mux.HandleFunc("/admin/email/test", a.withAdmin(a.handleEmailTest))
	mux.HandleFunc("/admin/fx/update", a.withAdmin(a.handleFxUpdate))
}

func registerSortRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/admin/sort/domains", a.withAdmin(a.handleSortDomains))
	mux.HandleFunc("/admin/sort/vps", a.withAdmin(a.handleSortVPS))
	mux.HandleFunc("/admin/sort/domains/alpha", a.withAdmin(a.handleSortDomainsAlpha))
	mux.HandleFunc("/admin/sort/domains/suffix", a.withAdmin(a.handleSortDomainsSuffix))
	mux.HandleFunc("/admin/sort/domains/expiry", a.withAdmin(a.handleSortDomainsExpiry))
	mux.HandleFunc("/admin/sort/domains/price", a.withAdmin(a.handleSortDomainsPrice))
	mux.HandleFunc("/admin/sort/domains/registrar", a.withAdmin(a.handleSortDomainsRegistrar))
	mux.HandleFunc("/admin/sort/vps/region", a.withAdmin(a.handleSortVPSRegion))
	mux.HandleFunc("/admin/sort/vps/expiry", a.withAdmin(a.handleSortVPSExpiry))
	mux.HandleFunc("/admin/sort/vps/vendor", a.withAdmin(a.handleSortVPSVendor))
	mux.HandleFunc("/admin/sort/vps/price", a.withAdmin(a.handleSortVPSPrice))
}

func registerBackupRoutes(mux *http.ServeMux, a *app) {
	mux.HandleFunc("/admin/backup/export", a.withAdmin(a.handleBackupExport))
	mux.HandleFunc("/admin/backup/import", a.withAdmin(a.handleBackupImport))
	mux.HandleFunc("/admin/backup/webdav/push", a.withAdmin(a.handleBackupWebDAVPush))
	mux.HandleFunc("/admin/backup/webdav/pull", a.withAdmin(a.handleBackupWebDAVPull))
}
