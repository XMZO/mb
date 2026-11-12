package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
)

// contentSecurityPolicy 只允许加载本站资源:页面里没有内联脚本和样式,也不依赖任何 CDN。
// img-src 额外放行 https,是为了支持用户自定义的 favicon。
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data: https:; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// recoverPanic 把处理器中的 panic 变成 500,避免连接被直接掐断。
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil && v != http.ErrAbortHandler {
				log.Printf("panic: %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				http.Error(w, "服务器内部错误", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// viewer 保护只读页面:公开模式下任何人可访问,私有模式下需要登录。
func (s *Server) viewer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.publicMode() && !s.isAdmin(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// admin 保护管理页面:未登录时跳转到登录页。
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// form 用于处理表单提交:限制请求体大小、解析表单并校验 CSRF token。
// 需要登录的表单应写成 s.admin(s.form(...)),先鉴权再解析请求体。
func (s *Server) form(next http.HandlerFunc) http.HandlerFunc {
	return s.formWithLimit(maxFormBytes, next)
}

func (s *Server) formWithLimit(limit int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)

		var err error
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			err = r.ParseMultipartForm(limit)
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
		} else {
			err = r.ParseForm()
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				s.renderError(w, r, http.StatusRequestEntityTooLarge, "提交的内容过大")
			} else {
				s.renderError(w, r, http.StatusBadRequest, "请求格式不正确")
			}
			return
		}
		if !s.validCSRF(r, r.PostForm.Get(csrfField)) {
			s.renderError(w, r, http.StatusForbidden, "页面已过期，请返回上一页刷新后重试")
			return
		}
		next(w, r)
	}
}

// api 用于页面脚本调用的 JSON 接口:需要登录,CSRF token 通过请求头传递,错误以 JSON 返回。
func (s *Server) api(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			writeJSONError(w, http.StatusUnauthorized, "登录已失效，请刷新页面重新登录")
			return
		}
		if !s.validCSRF(r, r.Header.Get(csrfHeader)) {
			writeJSONError(w, http.StatusForbidden, "页面已过期，请刷新后重试")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		next(w, r)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// isSecure 判断请求是否经由 HTTPS 到达(直连或经反向代理),用于决定 Cookie 的 Secure 属性。
func isSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
