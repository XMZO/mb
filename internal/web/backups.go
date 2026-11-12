package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mb/internal/backup"
)

// handleBackupExport 下载包含全部数据与设置的 JSON 备份。
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	data, err := backup.Export(r.Context(), s.store)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sendDownload(w, backup.Filename(time.Now()), "application/json; charset=utf-8", data)
}

// handleBackupImport 从上传的备份文件恢复数据。
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("backup_file")
	if err != nil {
		s.settingsFailed(w, r, panelBackup, "请选择备份文件", nil)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, backup.MaxBytes+1))
	if err != nil {
		s.settingsError(w, r, panelBackup, err)
		return
	}
	if int64(len(data)) > backup.MaxBytes {
		s.settingsFailed(w, r, panelBackup, "备份文件过大", nil)
		return
	}
	summary, err := s.restore(w, r, data, checked(r, "restore_settings"))
	if err != nil {
		s.settingsError(w, r, panelBackup, err)
		return
	}
	s.settingsSaved(w, r, panelBackup, "已从备份文件恢复："+summary)
}

// handleWebDAV 处理 WebDAV 面板上的三个按钮:保存配置、备份到 WebDAV、从 WebDAV 恢复。
// 三者都会先保存表单里的 WebDAV 配置。
func (s *Server) handleWebDAV(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	saved := backup.LoadWebDAV(s.store)
	dav := backup.WebDAV{
		URL:      strings.TrimSpace(f.Get("url")),
		Filename: strings.TrimSpace(f.Get("filename")),
		Username: strings.TrimSpace(f.Get("username")),
		Password: keepSecret(f.Get("password"), saved.Password, checked(r, "clear_password")),
	}
	if err := backup.SaveWebDAV(r.Context(), s.store, dav); err != nil {
		s.settingsError(w, r, panelBackup, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), slowActionTimeout)
	defer cancel()

	switch f.Get("action") {
	case "push":
		data, err := backup.Export(ctx, s.store)
		if err == nil {
			err = dav.Upload(ctx, data)
		}
		if err != nil {
			s.settingsError(w, r, panelBackup, err)
			return
		}
		s.settingsSaved(w, r, panelBackup, "已备份到 WebDAV")

	case "pull":
		data, err := dav.Download(ctx)
		if err != nil {
			s.settingsError(w, r, panelBackup, err)
			return
		}
		summary, err := s.restore(w, r, data, checked(r, "restore_settings"))
		if err != nil {
			s.settingsError(w, r, panelBackup, err)
			return
		}
		s.settingsSaved(w, r, panelBackup, "已从 WebDAV 恢复："+summary)

	default:
		s.settingsSaved(w, r, panelBackup, "WebDAV 配置已保存")
	}
}

// restore 用备份内容替换现有数据,返回形如"12 个域名、3 台 VPS"的摘要。
func (s *Server) restore(w http.ResponseWriter, r *http.Request, data []byte, withSettings bool) (string, error) {
	payload, err := backup.Decode(data)
	if err != nil {
		return "", err
	}
	// 恢复一旦开始就应该做完,不因浏览器断开而中途取消。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if err := backup.Restore(ctx, s.store, payload, withSettings); err != nil {
		return "", err
	}

	if withSettings {
		// 设置被整体替换:补齐缺失的必要项,重新应用环境变量指定的密码,
		// 并给当前浏览器重新签发会话(签名密钥可能在这个过程中更换)。
		if err := Bootstrap(ctx, s.store, s.cfg.AdminPassword); err != nil {
			return "", err
		}
		if err := s.fx.Init(ctx); err != nil {
			return "", err
		}
		s.startSession(w, r)
	}

	summary := fmt.Sprintf("%d 个域名、%d 台 VPS", len(payload.Domains), len(payload.VPS))
	if withSettings {
		summary += "，以及设置"
	}
	return summary, nil
}
