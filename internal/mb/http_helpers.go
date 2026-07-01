package mb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func parseIDQuery(r *http.Request) (int64, error) {
	idStr := strings.TrimSpace(r.URL.Query().Get("id"))
	if idStr == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
func readAllWithLimit(r io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(r, limit+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("文件过大")
	}
	return data, nil
}
func limitBodyWithMax(w http.ResponseWriter, r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return nil
}
func limitBody(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	return nil
}
func (a *app) serverError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	http.Error(w, "服务器内部错误", http.StatusInternalServerError)
}
func (a *app) settingsError(w http.ResponseWriter, userMsg string, err error) {
	if err != nil {
		log.Printf("settings error: %s: %v", userMsg, err)
	}
	a.renderSettingsMessage(w, userMsg, "")
}
func attachRenderMeta(data any, meta RenderMeta) any {
	switch v := data.(type) {
	case dashboardVM:
		v.RenderMeta = meta
		return v
	case loginVM:
		v.RenderMeta = meta
		return v
	case settingsVM:
		v.RenderMeta = meta
		return v
	case assetFormVM:
		v.RenderMeta = meta
		return v
	default:
		return data
	}
}

type metaResponseWriter interface {
	renderMeta() RenderMeta
}

func renderMetaFromWriter(w http.ResponseWriter) RenderMeta {
	if mw, ok := w.(metaResponseWriter); ok {
		return mw.renderMeta()
	}
	return RenderMeta{}
}

func (a *app) render(w http.ResponseWriter, name string, data any) {
	a.renderStatus(w, name, data, http.StatusOK)
}
func (a *app) renderStatus(w http.ResponseWriter, name string, data any, status int) {
	t, ok := a.templates[name]
	if !ok {
		a.serverError(w, fmt.Errorf("template %q not found", name))
		return
	}
	meta := renderMetaFromWriter(w)
	meta.AppVersion = displayVersion()
	meta.AppCommit = displayCommit()
	data = attachRenderMeta(data, meta)
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		a.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	_, _ = w.Write(buf.Bytes())
}
