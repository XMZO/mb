package mb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func (a *app) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload, err := a.buildBackup(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		a.serverError(w, err)
		return
	}
	filename := "mb-backup-" + time.Now().In(time.Local).Format("20060102-150405") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
func (a *app) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBodyWithMax(w, r, backupMaxBytes); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseMultipartForm(backupMaxBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("backup_file")
	if err != nil {
		a.renderSettingsMessage(w, "请选择备份文件", "")
		return
	}
	defer file.Close()

	data, err := readAllWithLimit(file, backupMaxBytes)
	if err != nil {
		a.settingsError(w, "读取备份文件失败,请查看服务端日志", err)
		return
	}

	payload, err := decodeBackup(data)
	if err != nil {
		a.renderSettingsMessage(w, "备份文件格式不正确："+err.Error(), "")
		return
	}

	restoreSettings := r.FormValue("restore_settings") == "1"
	if err := a.applyBackup(r.Context(), payload, restoreSettings); err != nil {
		a.settingsError(w, "导入失败,请检查备份内容或查看服务端日志", err)
		return
	}
	if err := ensureDefaults(a.db, a.cfg); err != nil {
		a.settingsError(w, "导入完成但初始化失败,请查看服务端日志", err)
		return
	}
	a.renderSettingsMessage(w, "", "已从本地备份恢复")
}
func (a *app) handleBackupWebDAVPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBodyWithMax(w, r, maxFormBodyBytes); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rawURL := strings.TrimSpace(r.FormValue("webdav_url"))
	filename := strings.TrimSpace(r.FormValue("webdav_filename"))
	user := r.FormValue("webdav_user")
	pass := r.FormValue("webdav_pass")
	a.persistWebDAVSettings(rawURL, filename, user, pass)

	signed := isSignedWebDAVURL(rawURL)
	if signed && (strings.HasSuffix(rawURL, "/") || filename != "") {
		a.renderSettingsMessage(w, "检测到签名 URL，请填写完整文件地址并清空文件名", "")
		return
	}

	targetURL := strings.TrimSpace(rawURL)
	if !signed {
		var err error
		targetURL, err = resolveWebDAVURL(rawURL, filename, true)
		if err != nil {
			a.renderSettingsMessage(w, err.Error(), "")
			return
		}
	}
	payload, err := a.buildBackup(r.Context())
	if err != nil {
		a.settingsError(w, "生成备份失败,请查看服务端日志", err)
		return
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		a.settingsError(w, "生成备份失败,请查看服务端日志", err)
		return
	}

	resp, err := doWebDAVRequest(http.MethodPut, targetURL, user, pass, data, "application/json", signed)
	if err != nil {
		a.settingsError(w, "WebDAV 备份失败,请检查配置或查看服务端日志", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readAllWithLimit(resp.Body, 1024)
		log.Printf("WebDAV backup failed: status=%s body=%q", resp.Status, strings.TrimSpace(string(body)))
		a.renderSettingsMessage(w, "WebDAV 备份失败:"+resp.Status, "")
		return
	}
	a.renderSettingsMessage(w, "", "已备份到 WebDAV")
}
func (a *app) handleBackupWebDAVPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBodyWithMax(w, r, maxFormBodyBytes); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rawURL := strings.TrimSpace(r.FormValue("webdav_url"))
	filename := strings.TrimSpace(r.FormValue("webdav_filename"))
	user := r.FormValue("webdav_user")
	pass := r.FormValue("webdav_pass")
	restoreSettings := r.FormValue("restore_settings") == "1"
	a.persistWebDAVSettings(rawURL, filename, user, pass)

	if strings.HasSuffix(rawURL, "/") && filename == "" {
		a.renderSettingsMessage(w, "恢复时请填写文件名", "")
		return
	}
	signed := isSignedWebDAVURL(rawURL)
	if signed && (strings.HasSuffix(rawURL, "/") || filename != "") {
		a.renderSettingsMessage(w, "检测到签名 URL，请填写完整文件地址并清空文件名", "")
		return
	}
	targetURL := strings.TrimSpace(rawURL)
	if !signed {
		var err error
		targetURL, err = resolveWebDAVURL(rawURL, filename, true)
		if err != nil {
			a.renderSettingsMessage(w, err.Error(), "")
			return
		}
	}

	resp, err := doWebDAVRequest(http.MethodGet, targetURL, user, pass, nil, "", signed)
	if err != nil {
		a.settingsError(w, "WebDAV 下载失败,请检查配置或查看服务端日志", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readAllWithLimit(resp.Body, 1024)
		log.Printf("WebDAV download failed: status=%s body=%q", resp.Status, strings.TrimSpace(string(body)))
		msg := resp.Status
		if resp.StatusCode == http.StatusMethodNotAllowed {
			msg = msg + "（请确认填写的是文件地址，或目录+文件名）"
		}
		a.renderSettingsMessage(w, "WebDAV 下载失败:"+msg, "")
		return
	}

	data, err := readAllWithLimit(resp.Body, backupMaxBytes)
	if err != nil {
		a.settingsError(w, "读取 WebDAV 备份失败,请查看服务端日志", err)
		return
	}
	payload, err := decodeBackup(data)
	if err != nil {
		a.renderSettingsMessage(w, "备份文件格式不正确："+err.Error(), "")
		return
	}
	if err := a.applyBackup(r.Context(), payload, restoreSettings); err != nil {
		a.settingsError(w, "恢复失败,请检查备份内容或查看服务端日志", err)
		return
	}
	if err := ensureDefaults(a.db, a.cfg); err != nil {
		a.settingsError(w, "恢复完成但初始化失败,请查看服务端日志", err)
		return
	}
	a.renderSettingsMessage(w, "", "已从 WebDAV 恢复")
}
func (a *app) buildBackup(ctx context.Context) (backupPayload, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	payload := backupPayload{
		Version:    backupVersion,
		ExportedAt: time.Now().In(time.Local).Format(time.RFC3339),
		Settings:   make(map[string]string),
	}

	rows, err := a.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return payload, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return payload, err
		}
		payload.Settings[k] = v
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return payload, err
	}
	rows.Close()

	rows, err = a.db.QueryContext(ctx, `SELECT domain, registrar, expires_at, renewal_price, currency, notes, exclude_cost, pinned, hidden, sort_order, created_at, updated_at FROM domains ORDER BY id ASC`)
	if err != nil {
		return payload, err
	}
	for rows.Next() {
		var d backupDomain
		if err := rows.Scan(&d.Domain, &d.Registrar, &d.ExpiresAt, &d.RenewalPrice, &d.Currency, &d.Notes, &d.ExcludeCost, &d.Pinned, &d.Hidden, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt); err != nil {
			rows.Close()
			return payload, err
		}
		d.ExpiresAt = normalizeISODateOrPermanent(d.ExpiresAt)
		payload.Domains = append(payload.Domains, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return payload, err
	}
	rows.Close()

	rows, err = a.db.QueryContext(ctx, `SELECT vendor, ip, specs, region, cores, memory_gb, disk_gb, disk_type, disk2_gb, disk2_type, disks_json, traffic_tb, traffic_set, traffic_direction, bandwidth_value, bandwidth_unit, bandwidth_mbps, expires_at, renewal_price, currency, notes, exclude_cost, billing_months, pinned, hidden, sort_order, created_at, updated_at FROM vps ORDER BY id ASC`)
	if err != nil {
		return payload, err
	}
	for rows.Next() {
		var v backupVPS
		if err := rows.Scan(&v.Vendor, &v.IP, &v.Specs, &v.Region, &v.Cores, &v.MemoryGB, &v.DiskGB, &v.DiskType, &v.Disk2GB, &v.Disk2Type, &v.DisksJSON, &v.TrafficTB, &v.TrafficSet, &v.TrafficDir, &v.BandwidthValue, &v.BandwidthUnit, &v.BandwidthMbps, &v.ExpiresAt, &v.RenewalPrice, &v.Currency, &v.Notes, &v.ExcludeCost, &v.BillingMonths, &v.Pinned, &v.Hidden, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return payload, err
		}
		v.ExpiresAt = normalizeISODateOrPermanent(v.ExpiresAt)
		payload.VPS = append(payload.VPS, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return payload, err
	}
	rows.Close()

	return payload, nil
}
func (a *app) applyBackup(ctx context.Context, payload backupPayload, restoreSettings bool) error {
	version := payload.Version
	if version == 0 {
		version = backupVersion
	}
	if version != backupVersion {
		return fmt.Errorf("unsupported backup version: %d", version)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if restoreSettings {
		if _, err = tx.ExecContext(ctx, `DELETE FROM settings`); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM domains`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM vps`); err != nil {
		return err
	}

	if restoreSettings {
		for k, v := range payload.Settings {
			if strings.TrimSpace(k) == "" {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)`, k, v); err != nil {
				return err
			}
		}
	}

	now := time.Now().In(time.Local).Format(time.RFC3339)
	maxDomainSort := 0
	for _, d := range payload.Domains {
		if d.SortOrder > maxDomainSort {
			maxDomainSort = d.SortOrder
		}
	}
	maxVPSSort := 0
	for _, v := range payload.VPS {
		if v.SortOrder > maxVPSSort {
			maxVPSSort = v.SortOrder
		}
	}
	for _, d := range payload.Domains {
		expiresAt, err := normalizeBackupExpiresAt(d.ExpiresAt)
		if err != nil {
			return fmt.Errorf("domain expires_at invalid: %w", err)
		}
		currency := strings.ToUpper(strings.TrimSpace(d.Currency))
		if !isSupportedCurrency(currency) {
			currency = "CNY"
		}
		price := d.RenewalPrice
		if !isFiniteNonNegative(price) {
			price = 0
		}
		excludeCost := 0
		if d.ExcludeCost != 0 {
			excludeCost = 1
		}
		pinned := 0
		if d.Pinned != 0 {
			pinned = 1
		}
		hidden := 0
		if d.Hidden != 0 {
			hidden = 1
		}
		sortOrder := d.SortOrder
		if sortOrder <= 0 {
			maxDomainSort++
			sortOrder = maxDomainSort
		}
		createdAt := normalizeRFC3339OrNow(d.CreatedAt, now)
		updatedAt := normalizeRFC3339OrNow(d.UpdatedAt, now)
		if _, err = tx.ExecContext(ctx, insertDomainSQL,
			d.Domain, d.Registrar, expiresAt, price, currency, d.Notes, excludeCost, pinned, hidden, sortOrder, createdAt, updatedAt); err != nil {
			return err
		}
	}

	for _, v := range payload.VPS {
		expiresAt, err := normalizeBackupExpiresAt(v.ExpiresAt)
		if err != nil {
			return fmt.Errorf("vps expires_at invalid: %w", err)
		}
		currency := strings.ToUpper(strings.TrimSpace(v.Currency))
		if !isSupportedCurrency(currency) {
			currency = "CNY"
		}
		price := v.RenewalPrice
		if !isFiniteNonNegative(price) {
			price = 0
		}
		trafficSet := 0
		if v.TrafficSet != 0 {
			trafficSet = 1
		}
		trafficDir := normalizeTrafficDir(v.TrafficDir)
		if trafficSet == 0 {
			trafficDir = ""
		}
		bandwidthUnit := normalizeBandwidthUnit(v.BandwidthUnit)
		bwValue := v.BandwidthValue
		if !isFiniteNonNegative(bwValue) {
			bwValue = 0
		}
		bwMbps := v.BandwidthMbps
		if bwMbps < 0 {
			bwMbps = 0
		}
		memoryGB := v.MemoryGB
		if !isFiniteNonNegative(memoryGB) {
			memoryGB = 0
		}
		trafficTB := v.TrafficTB
		if !isFiniteNonNegative(trafficTB) {
			trafficTB = 0
		}
		excludeCost := 0
		if v.ExcludeCost != 0 {
			excludeCost = 1
		}
		pinned := 0
		if v.Pinned != 0 {
			pinned = 1
		}
		hidden := 0
		if v.Hidden != 0 {
			hidden = 1
		}
		billingMonths := v.BillingMonths
		if billingMonths <= 0 {
			billingMonths = 12
		}
		sortOrder := v.SortOrder
		if sortOrder <= 0 {
			maxVPSSort++
			sortOrder = maxVPSSort
		}
		disksJSON := normalizeDisksJSON(v.DisksJSON)
		diskType := normalizeDiskType(v.DiskType)
		disk2Type := normalizeDiskType(v.Disk2Type)
		createdAt := normalizeRFC3339OrNow(v.CreatedAt, now)
		updatedAt := normalizeRFC3339OrNow(v.UpdatedAt, now)
		if _, err = tx.ExecContext(ctx, insertVPSSQL,
			v.Vendor, v.IP, v.Specs, v.Region, maxZeroInt(v.Cores), memoryGB, maxZeroInt(v.DiskGB), diskType, maxZeroInt(v.Disk2GB), disk2Type, disksJSON, trafficTB, trafficSet, trafficDir, bwValue, bandwidthUnit, bwMbps, expiresAt, price, currency, v.Notes, excludeCost, billingMonths, pinned, hidden, sortOrder, createdAt, updatedAt); err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	if restoreSettings {
		invalidateSettingsCache(a.db)
	}
	return nil
}
func decodeBackup(data []byte) (backupPayload, error) {
	var payload backupPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}
func normalizeBackupExpiresAt(expiresAt string) (string, error) {
	expiresAt = normalizeISODateOrPermanent(expiresAt)
	if expiresAt == "" {
		return "", errors.New("missing expires_at")
	}
	if isPermanentISODate(expiresAt) {
		return expiresAt, nil
	}
	if _, err := parseISODate(expiresAt); err != nil {
		return "", err
	}
	return expiresAt, nil
}
func normalizeRFC3339OrNow(raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.Format(time.RFC3339)
	}
	return fallback
}
func normalizeDisksJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var disks []diskSpec
	if err := json.Unmarshal([]byte(raw), &disks); err != nil {
		return ""
	}
	var cleaned []diskSpec
	for _, d := range disks {
		if d.SizeGB <= 0 {
			continue
		}
		cleaned = append(cleaned, diskSpec{
			SizeGB: d.SizeGB,
			Type:   normalizeDiskType(d.Type),
		})
	}
	if len(cleaned) == 0 {
		return ""
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return ""
	}
	return string(b)
}
