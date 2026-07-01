package mb

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

func applyEmailFormToVM(vm *settingsVM, form emailSettingsForm) {
	vm.EmailEnabled = form.Enabled
	vm.EmailDays = form.DaysBeforeRaw
	vm.EmailProtocol = form.Protocol
	vm.EmailHost = form.Host
	vm.EmailPort = form.Port
	vm.EmailEncryption = form.Encryption
	vm.EmailUser = form.User
	vm.EmailPass = form.Pass
	vm.EmailFrom = form.From
	vm.EmailTo = form.To
}
func persistEmailSettings(db *sql.DB, form emailSettingsForm) error {
	if err := setSetting(db, settingEmailEnabled, boolTo01(form.Enabled)); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailDays, form.DaysBeforeRaw); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailProtocol, form.Protocol); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailHost, form.Host); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailPort, form.Port); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailEncrypt, form.Encryption); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailUser, form.User); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailPass, form.Pass); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailFrom, form.From); err != nil {
		return err
	}
	if err := setSetting(db, settingEmailTo, form.To); err != nil {
		return err
	}
	return nil
}
func parseEmailSettingsForm(r *http.Request) (emailSettingsForm, string) {
	form := emailSettingsForm{
		Enabled:    r.FormValue("email_enabled") == "1",
		Protocol:   strings.TrimSpace(r.FormValue("email_protocol")),
		Host:       strings.TrimSpace(r.FormValue("email_host")),
		Port:       strings.TrimSpace(r.FormValue("email_port")),
		Encryption: strings.TrimSpace(r.FormValue("email_encryption")),
		User:       strings.TrimSpace(r.FormValue("email_user")),
		Pass:       r.FormValue("email_pass"),
		From:       strings.TrimSpace(r.FormValue("email_from")),
		To:         strings.TrimSpace(r.FormValue("email_to")),
	}

	daysStr := strings.TrimSpace(r.FormValue("email_days_before"))
	daysList, normalizedDays, err := parseReminderDaysList(daysStr)
	if err != nil {
		form.DaysBeforeRaw = daysStr
		return form, "提醒天数格式不正确"
	}
	form.DaysBefore = daysList
	form.DaysBeforeRaw = normalizedDays

	protocol, err := normalizeEmailProtocol(form.Protocol)
	if err != nil {
		return form, "邮件协议仅支持 SMTP/SMTPS"
	}
	form.Protocol = protocol

	encryption, err := normalizeEmailEncryption(form.Encryption)
	if err != nil {
		return form, "加密方式仅支持 自动/无/STARTTLS/SSL"
	}
	form.Encryption = encryption

	if form.Port != "" {
		p, err := strconv.Atoi(form.Port)
		if err != nil || p <= 0 || p > 65535 {
			return form, "端口格式不正确"
		}
	}
	return form, ""
}
func parseReminderDaysList(raw string) ([]int, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = defaultEmailDaysList
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\n', '\t', '、':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return nil, "", errors.New("empty days list")
	}
	seen := make(map[int]struct{}, len(parts))
	days := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		val, err := strconv.Atoi(part)
		if err != nil || val < 0 {
			return nil, "", errors.New("invalid days")
		}
		if _, ok := seen[val]; ok {
			continue
		}
		seen[val] = struct{}{}
		days = append(days, val)
	}
	if len(days) == 0 {
		return nil, "", errors.New("empty days list")
	}
	normalized := make([]string, 0, len(days))
	for _, day := range days {
		normalized = append(normalized, strconv.Itoa(day))
	}
	return days, strings.Join(normalized, ","), nil
}
func normalizeEmailProtocol(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	switch raw {
	case "smtp", "smtps":
		return raw, nil
	default:
		return "", errors.New("invalid protocol")
	}
}
func normalizeEmailEncryption(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "auto", nil
	}
	switch raw {
	case "auto":
		return "auto", nil
	case "none", "plain":
		return "none", nil
	case "starttls", "tls":
		return "starttls", nil
	case "ssl", "ssltls":
		return "ssl", nil
	default:
		return "", errors.New("invalid encryption")
	}
}
func splitHostPort(rawHost, rawPort string) (string, string) {
	host := strings.TrimSpace(rawHost)
	port := strings.TrimSpace(rawPort)
	if host == "" || port != "" {
		return host, port
	}
	if strings.Contains(host, ":") {
		h, p, err := net.SplitHostPort(host)
		if err == nil {
			return h, p
		}
	}
	return host, port
}
func defaultEmailPort(protocol, encryption string) int {
	if protocol == "smtps" || encryption == "ssl" {
		return 465
	}
	if encryption == "none" {
		return 25
	}
	return 587
}
func buildEmailConfig(form emailSettingsForm) (emailConfig, error) {
	protocol := strings.TrimSpace(form.Protocol)
	if protocol == "" {
		protocol = "smtp"
	}
	encryption := strings.TrimSpace(form.Encryption)
	if encryption == "" {
		encryption = "auto"
	}

	host, portStr := splitHostPort(form.Host, form.Port)
	if host == "" {
		return emailConfig{}, errors.New("请填写邮件服务器地址")
	}

	port := 0
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 || p > 65535 {
			return emailConfig{}, errors.New("端口格式不正确")
		}
		port = p
	}
	if port == 0 {
		port = defaultEmailPort(protocol, encryption)
	}

	fromRaw := strings.TrimSpace(form.From)
	if fromRaw == "" {
		fromRaw = strings.TrimSpace(form.User)
	}
	if fromRaw == "" {
		return emailConfig{}, errors.New("请填写发件邮箱")
	}
	fromAddr, err := mail.ParseAddress(fromRaw)
	if err != nil {
		return emailConfig{}, errors.New("发件邮箱格式不正确")
	}

	toAddrs, err := parseEmailAddressList(form.To)
	if err != nil {
		return emailConfig{}, err
	}

	return emailConfig{
		Protocol:   protocol,
		Host:       host,
		Port:       port,
		Encryption: encryption,
		Username:   strings.TrimSpace(form.User),
		Password:   form.Pass,
		From:       *fromAddr,
		To:         toAddrs,
	}, nil
}
func parseEmailAddressList(raw string) ([]mail.Address, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("请填写接收邮箱")
	}
	cleaned := strings.ReplaceAll(raw, ";", ",")
	cleaned = strings.ReplaceAll(cleaned, "\n", ",")
	list, err := mail.ParseAddressList(cleaned)
	if err != nil || len(list) == 0 {
		return nil, errors.New("接收邮箱格式不正确")
	}
	out := make([]mail.Address, 0, len(list))
	for _, addr := range list {
		out = append(out, *addr)
	}
	return out, nil
}
func (a *app) getEmailSettingsForm() emailSettingsForm {
	rawDays := mustGetStringSetting(a.db, settingEmailDays, defaultEmailDaysList)
	daysList, normalizedDays, err := parseReminderDaysList(rawDays)
	if err != nil {
		daysList, normalizedDays, _ = parseReminderDaysList(defaultEmailDaysList)
	}
	return emailSettingsForm{
		Enabled:       mustGetBoolSetting(a.db, settingEmailEnabled, false),
		DaysBefore:    daysList,
		DaysBeforeRaw: normalizedDays,
		Protocol:      mustGetStringSetting(a.db, settingEmailProtocol, ""),
		Host:          mustGetStringSetting(a.db, settingEmailHost, ""),
		Port:          mustGetStringSetting(a.db, settingEmailPort, ""),
		Encryption:    mustGetStringSetting(a.db, settingEmailEncrypt, "auto"),
		User:          mustGetStringSetting(a.db, settingEmailUser, ""),
		Pass:          mustGetStringSetting(a.db, settingEmailPass, ""),
		From:          mustGetStringSetting(a.db, settingEmailFrom, ""),
		To:            mustGetStringSetting(a.db, settingEmailTo, ""),
	}
}
func (a *app) startReminderLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		if err := a.runReminderCheck(context.Background()); err != nil {
			log.Printf("reminder check failed: %v", err)
		}
		<-ticker.C
	}
}
func (a *app) runReminderCheck(ctx context.Context) error {
	form := a.getEmailSettingsForm()
	if !form.Enabled {
		return nil
	}
	cfg, err := buildEmailConfig(form)
	if err != nil {
		return err
	}
	now := time.Now().In(time.Local)
	sentDate := now.Format("2006-01-02")

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	for _, daysBefore := range form.DaysBefore {
		if daysBefore < 0 {
			continue
		}
		sent, err := a.loadReminderLog(ctx, daysBefore, sentDate)
		if err != nil {
			return err
		}
		items, err := a.collectReminderItems(ctx, daysBefore, sent)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			continue
		}
		subject, body := buildReminderMessage(daysBefore, items, sentDate)
		if err := sendEmail(cfg, subject, body); err != nil {
			return err
		}
		if err := a.saveReminderLog(ctx, items, daysBefore, sentDate); err != nil {
			return err
		}
	}
	return nil
}
func (a *app) loadReminderLog(ctx context.Context, daysBefore int, sentDate string) (map[string]struct{}, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT item_type, item_id FROM reminder_log WHERE days_before = ? AND sent_date = ?`, daysBefore, sentDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sent := make(map[string]struct{})
	for rows.Next() {
		var itemType string
		var itemID int64
		if err := rows.Scan(&itemType, &itemID); err != nil {
			return nil, err
		}
		sent[reminderKey(itemType, itemID)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sent, nil
}
func (a *app) collectReminderItems(ctx context.Context, daysBefore int, sent map[string]struct{}) ([]reminderItem, error) {
	var items []reminderItem

	rows, err := a.db.QueryContext(ctx, `SELECT id, domain, registrar, expires_at, renewal_price, currency FROM domains ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var domain, registrar, expiresAt, currency string
		var price float64
		if err := rows.Scan(&id, &domain, &registrar, &expiresAt, &price, &currency); err != nil {
			rows.Close()
			return nil, err
		}
		expiresAt = normalizeISODateOrPermanent(expiresAt)
		if isPermanentISODate(expiresAt) {
			continue
		}
		if _, err := parseISODate(expiresAt); err != nil {
			continue
		}
		if daysUntilISODate(expiresAt) != daysBefore {
			continue
		}
		if _, ok := sent[reminderKey("domain", id)]; ok {
			continue
		}
		title := domain
		if registrar != "" {
			title = fmt.Sprintf("%s (%s)", domain, registrar)
		}
		items = append(items, reminderItem{
			Type:      "domain",
			ID:        id,
			Title:     title,
			ExpiresAt: expiresAt,
			Price:     price,
			Currency:  currency,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = a.db.QueryContext(ctx, `SELECT id, vendor, ip, expires_at, renewal_price, currency FROM vps ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var vendor, ip, expiresAt, currency string
		var price float64
		if err := rows.Scan(&id, &vendor, &ip, &expiresAt, &price, &currency); err != nil {
			rows.Close()
			return nil, err
		}
		expiresAt = normalizeISODateOrPermanent(expiresAt)
		if isPermanentISODate(expiresAt) {
			continue
		}
		if _, err := parseISODate(expiresAt); err != nil {
			continue
		}
		if daysUntilISODate(expiresAt) != daysBefore {
			continue
		}
		if _, ok := sent[reminderKey("vps", id)]; ok {
			continue
		}
		title := vendor
		if ip != "" {
			title = fmt.Sprintf("%s (%s)", vendor, ip)
		}
		items = append(items, reminderItem{
			Type:      "vps",
			ID:        id,
			Title:     title,
			ExpiresAt: expiresAt,
			Price:     price,
			Currency:  currency,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	return items, nil
}
func (a *app) saveReminderLog(ctx context.Context, items []reminderItem, daysBefore int, sentDate string) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reminder_log(item_type, item_id, days_before, sent_date, created_at) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().In(time.Local).Format(time.RFC3339)
	for _, item := range items {
		if _, err := stmt.ExecContext(ctx, item.Type, item.ID, daysBefore, sentDate, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func reminderKey(itemType string, id int64) string {
	return fmt.Sprintf("%s:%d", itemType, id)
}
func buildReminderMessage(daysBefore int, items []reminderItem, sentDate string) (string, string) {
	var domainLines []string
	var vpsLines []string

	for _, item := range items {
		line := fmt.Sprintf("- %s | %s | %s", item.Title, item.ExpiresAt, reminderPriceLabel(item.Price, item.Currency))
		switch item.Type {
		case "domain":
			domainLines = append(domainLines, line)
		case "vps":
			vpsLines = append(vpsLines, line)
		}
	}

	subject := fmt.Sprintf("到期提醒：%d 项资产将在 %d 天后到期", len(items), daysBefore)
	if daysBefore == 0 {
		subject = fmt.Sprintf("到期提醒：%d 项资产今日到期", len(items))
	}

	var b strings.Builder
	if daysBefore == 0 {
		b.WriteString("到期提醒（今日到期）\n")
	} else {
		b.WriteString(fmt.Sprintf("到期提醒（提前 %d 天）\n", daysBefore))
	}
	b.WriteString("日期：" + sentDate + "\n\n")
	if len(domainLines) > 0 {
		b.WriteString("域名：\n")
		b.WriteString(strings.Join(domainLines, "\n"))
		b.WriteString("\n\n")
	}
	if len(vpsLines) > 0 {
		b.WriteString("VPS：\n")
		b.WriteString(strings.Join(vpsLines, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("来自：MB 资产面板\n")
	return subject, b.String()
}
func reminderPriceLabel(price float64, currency string) string {
	if price == 0 {
		return "免费"
	}
	return formatFloat(price) + " " + strings.TrimSpace(currency)
}
func sendEmail(cfg emailConfig, subject, body string) error {
	client, err := dialSMTP(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	if err := client.Mail(cfg.From.Address); err != nil {
		return err
	}
	for _, addr := range cfg.To {
		if err := client.Rcpt(addr.Address); err != nil {
			return err
		}
	}

	w, err := client.Data()
	if err != nil {
		return err
	}
	msg := buildEmailMessage(cfg, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return nil
}
func dialSMTP(cfg emailConfig) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsConfig := &tls.Config{ServerName: cfg.Host}

	switch cfg.Encryption {
	case "ssl":
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if err != nil {
			return nil, err
		}
		setConnDeadline(conn, smtpTimeout)
		return smtp.NewClient(conn, cfg.Host)
	case "starttls":
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		setConnDeadline(conn, smtpTimeout)
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if ok, _ := client.Extension("STARTTLS"); !ok {
			client.Close()
			return nil, errors.New("服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			client.Close()
			return nil, err
		}
		setConnDeadline(conn, smtpTimeout)
		return client, nil
	case "none":
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		setConnDeadline(conn, smtpTimeout)
		return smtp.NewClient(conn, cfg.Host)
	case "auto":
		if cfg.Protocol == "smtps" || cfg.Port == 465 {
			conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
			if err != nil {
				return nil, err
			}
			setConnDeadline(conn, smtpTimeout)
			return smtp.NewClient(conn, cfg.Host)
		}
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		setConnDeadline(conn, smtpTimeout)
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				client.Close()
				return nil, err
			}
			setConnDeadline(conn, smtpTimeout)
		}
		return client, nil
	default:
		return nil, errors.New("加密方式不支持")
	}
}
func setConnDeadline(conn net.Conn, timeout time.Duration) {
	if conn == nil || timeout <= 0 {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
}
func buildEmailMessage(cfg emailConfig, subject, body string) string {
	from := sanitizeHeaderValue(cfg.From.String())
	to := sanitizeHeaderValue(joinAddressList(cfg.To))
	subject = sanitizeHeaderValue(subject)

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
func joinAddressList(list []mail.Address) string {
	parts := make([]string, 0, len(list))
	for _, addr := range list {
		parts = append(parts, addr.String())
	}
	return strings.Join(parts, ", ")
}
func sanitizeHeaderValue(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "\n", " ")
	return strings.TrimSpace(v)
}
