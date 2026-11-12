package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"mb/internal/buildinfo"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// pageNames 是全部页面模板;每个页面由 base.html、partials.html 加上自己的文件组成。
var pageNames = []string{"dashboard", "login", "domain_form", "vps_form", "settings", "error"}

func parsePages() (map[string]*template.Template, error) {
	version, err := staticVersion()
	if err != nil {
		return nil, err
	}
	funcs := template.FuncMap{
		// static 生成带内容指纹的静态资源地址,资源可以被浏览器永久缓存。
		"static": func(name string) string { return "/static/" + name + "?v=" + version },
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(funcs).ParseFS(templateFS,
			"templates/base.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	return pages, nil
}

// staticVersion 返回全部静态资源内容的指纹。
func staticVersion() (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(staticFS, "static", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := staticFS.ReadFile(name)
		hash.Write([]byte(name))
		hash.Write(data)
		return err
	})
	return hex.EncodeToString(hash.Sum(nil))[:12], err
}

// staticTypes 显式指定 Content-Type:Windows 上的 MIME 注册表可能把 .js 映射成别的类型,
// 而页面开启了 nosniff,类型不对脚本就不会执行。
var staticTypes = map[string]string{
	".css": "text/css; charset=utf-8",
	".js":  "text/javascript; charset=utf-8",
	".svg": "image/svg+xml",
}

func staticHandler() http.Handler {
	files, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // 目录是编译期嵌入的,不可能不存在
	}
	server := http.FileServerFS(files)
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // 不提供目录列表
			return
		}
		if contentType, ok := staticTypes[path.Ext(r.URL.Path)]; ok {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		server.ServeHTTP(w, r)
	}))
}

// base 是所有页面共有的数据,由 render 统一填充;各页面的数据结构内嵌它。
type base struct {
	Title      string // 由各页面自行设置
	IsAdmin    bool
	PublicMode bool
	FaviconURL string
	CSRFToken  string
	Version    string
	Commit     string
	Flash      *flash
}

func (b *base) common() *base { return b }

// page 是可以交给 render 渲染的页面数据。
type page interface {
	common() *base
}

// render 填充公共数据并输出页面。模板先渲染到缓冲区,出错时不会输出半截页面。
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data page) {
	b := data.common()
	b.IsAdmin = s.isAdmin(r)
	b.PublicMode = s.publicMode()
	b.FaviconURL = s.faviconURL()
	b.CSRFToken = s.csrfToken(w, r)
	b.Version = buildinfo.DisplayVersion()
	b.Commit = buildinfo.ShortCommit()
	if status < http.StatusBadRequest {
		b.Flash = s.popFlash(w, r) // 错误页不消费提示,留给下一个正常页面
	}
	if b.IsAdmin {
		s.renewSession(w, r)
	}

	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, "base", data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

type errorPage struct {
	base
	Status  int
	Message string
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.render(w, r, status, "error", &errorPage{
		base:    base{Title: http.StatusText(status)},
		Status:  status,
		Message: message,
	})
}

// serverError 记录内部错误的细节,对外只显示统一的提示。
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	s.renderError(w, r, http.StatusInternalServerError, "服务器内部错误，详情请查看服务端日志")
}

// ---- 一次性提示 ----
//
// 表单处理成功后先重定向再展示结果(PRG),避免刷新页面时重复提交。
// 提示内容通过一个短命的 Cookie 带到下一个页面,读出后立即清除。

type flash struct {
	Kind    string // flashOK / flashError
	Message string
}

const (
	flashOK    = "ok"
	flashError = "error"
)

func (s *Server) flashCookieName() string { return s.cfg.CookieName + "_flash" }

func (s *Server) setFlash(w http.ResponseWriter, r *http.Request, kind, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.flashCookieName(),
		Value:    base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + message)),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure(r),
	})
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := r.Cookie(s.flashCookieName())
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: s.flashCookieName(), Path: "/", MaxAge: -1, HttpOnly: true})

	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	kind, message, ok := strings.Cut(string(raw), ":")
	if !ok || message == "" || (kind != flashOK && kind != flashError) {
		return nil
	}
	return &flash{Kind: kind, Message: message}
}

// redirectWithFlash 设置一次性提示并跳转。
func (s *Server) redirectWithFlash(w http.ResponseWriter, r *http.Request, target, kind, message string) {
	s.setFlash(w, r, kind, message)
	http.Redirect(w, r, target, http.StatusSeeOther)
}
