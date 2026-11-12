// Package web 是 HTTP 层:路由、中间件、页面渲染,以及各功能的请求处理。
package web

import (
	"context"
	"html/template"
	"net/http"

	"mb/internal/backup"
	"mb/internal/config"
	"mb/internal/fx"
	"mb/internal/mailer"
	"mb/internal/store"
)

// maxFormBytes 是普通表单与 JSON 请求体的大小上限。
const maxFormBytes int64 = 1 << 20

// Server 持有处理请求所需的全部依赖。
type Server struct {
	cfg     config.Config
	store   *store.Store
	fx      *fx.Service
	limiter *loginLimiter
	pages   map[string]*template.Template

	sendMail func(context.Context, mailer.Config, mailer.Message) error // 测试中可替换
}

// New 创建 Server 并解析内嵌的页面模板。
func New(cfg config.Config, st *store.Store, rates *fx.Service) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:      cfg,
		store:    st,
		fx:       rates,
		limiter:  newLoginLimiter(cfg.Login),
		pages:    pages,
		sendMail: mailer.Send,
	}, nil
}

// Handler 返回注册了全部路由的 HTTP 处理器。
//
// 路由约定:
//   - viewer:公开模式下任何人可访问,私有模式下需要登录;
//   - admin: 需要登录,未登录时跳转到登录页;
//   - form:  解析表单并校验 CSRF token;
//   - api:   供页面脚本调用的 JSON 接口,需要登录,CSRF token 放在请求头里。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("GET /{$}", s.viewer(s.handleDashboard))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.form(s.handleLogin))
	mux.HandleFunc("POST /logout", s.form(s.handleLogout))

	mux.HandleFunc("GET /domains/new", s.admin(s.handleDomainNew))
	mux.HandleFunc("POST /domains/new", s.admin(s.form(s.handleDomainCreate)))
	mux.HandleFunc("GET /domains/{id}/edit", s.admin(s.handleDomainEdit))
	mux.HandleFunc("POST /domains/{id}/edit", s.admin(s.form(s.handleDomainUpdate)))
	mux.HandleFunc("POST /domains/export", s.admin(s.form(s.handleDomainExport)))

	mux.HandleFunc("GET /vps/new", s.admin(s.handleVPSNew))
	mux.HandleFunc("POST /vps/new", s.admin(s.form(s.handleVPSCreate)))
	mux.HandleFunc("GET /vps/{id}/edit", s.admin(s.handleVPSEdit))
	mux.HandleFunc("POST /vps/{id}/edit", s.admin(s.form(s.handleVPSUpdate)))

	mux.HandleFunc("POST /api/{kind}/batch", s.api(s.handleBatch))
	mux.HandleFunc("POST /api/{kind}/order", s.api(s.handleReorder))

	mux.HandleFunc("GET /admin/settings", s.admin(s.handleSettings))
	mux.HandleFunc("POST /admin/settings/visibility", s.admin(s.form(s.handleSaveVisibility)))
	mux.HandleFunc("POST /admin/settings/fx", s.admin(s.form(s.handleSaveFX)))
	mux.HandleFunc("POST /admin/settings/fx/refresh", s.admin(s.form(s.handleRefreshFX)))
	mux.HandleFunc("POST /admin/settings/password", s.admin(s.form(s.handleChangePassword)))
	mux.HandleFunc("POST /admin/settings/email", s.admin(s.form(s.handleSaveEmail)))

	mux.HandleFunc("GET /admin/backup/export", s.admin(s.handleBackupExport))
	mux.HandleFunc("POST /admin/backup/import", s.admin(s.formWithLimit(backup.MaxBytes+maxFormBytes, s.handleBackupImport)))
	mux.HandleFunc("POST /admin/backup/webdav", s.admin(s.form(s.handleWebDAV)))

	mux.HandleFunc("/", s.handleNotFound)

	return s.recoverPanic(securityHeaders(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "页面不存在")
}
