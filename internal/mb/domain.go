package mb

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *app) loadDomains(ctx context.Context, fx fxInfo, baseCurrency string) ([]domainRow, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := a.db.QueryContext(ctx, `SELECT id, domain, registrar, expires_at, renewal_price, currency, notes, exclude_cost, pinned, hidden FROM domains
		ORDER BY pinned DESC, (sort_order = 0) ASC, sort_order ASC, expires_at ASC, id ASC`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []domainRow
	var total float64
	for rows.Next() {
		var r domainRow
		var excludeCost int
		var pinned int
		var hidden int
		if err := rows.Scan(&r.ID, &r.Domain, &r.Registrar, &r.ExpiresAt, &r.RenewalPrice, &r.Currency, &r.Notes, &excludeCost, &pinned, &hidden); err != nil {
			return nil, 0, err
		}
		r.ExcludeCost = excludeCost != 0
		r.Pinned = pinned != 0
		r.Hidden = hidden != 0
		r.ExpiresAt = normalizeISODateOrPermanent(r.ExpiresAt)
		if isPermanentISODate(r.ExpiresAt) {
			r.IsPermanent = true
			r.DaysLeft = -1
			r.DaysBadgeClass = "text-bg-success"
		} else {
			days := daysUntilISODate(r.ExpiresAt)
			r.DaysLeft = days
			r.DaysBadgeClass = daysBadgeClass(days)
		}
		r.RenewalPriceBase = convertMoney(r.RenewalPrice, r.Currency, baseCurrency, fx)
		annualBase := r.RenewalPriceBase
		if r.ExcludeCost {
			annualBase = 0
		}
		total += annualBase
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
func (a *app) loadDomainItem(ctx context.Context, id int64) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var (
		domain    string
		registrar string
		expiresAt string
		price     float64
		currency  string
		notes     string
		exclude   int
		pinned    int
		hidden    int
	)
	err := a.db.QueryRowContext(ctx, `SELECT domain, registrar, expires_at, renewal_price, currency, notes, exclude_cost, pinned, hidden FROM domains WHERE id=?`, id).
		Scan(&domain, &registrar, &expiresAt, &price, &currency, &notes, &exclude, &pinned, &hidden)
	if err != nil {
		return nil, err
	}
	expiresAt = normalizeISODateOrPermanent(expiresAt)
	return map[string]string{
		"domain":            domain,
		"registrar":         registrar,
		"expires_at":        expiresAt,
		"expires_permanent": boolTo01(isPermanentISODate(expiresAt)),
		"price":             formatFloat(price),
		"currency":          currency,
		"notes":             notes,
		"exclude_cost":      boolTo01(exclude != 0),
		"pinned":            boolTo01(pinned != 0),
		"hidden":            boolTo01(hidden != 0),
	}, nil
}
func (a *app) handleDomainNew(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()
	if r.Method == http.MethodGet {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "domain",
			Title:      "新增域名",
			Action:     "/domain/new",
			Item: map[string]string{
				"domain":            "",
				"registrar":         "",
				"expires_at":        time.Now().In(time.Local).AddDate(1, 0, 0).Format("2006-01-02"),
				"expires_permanent": "0",
				"price":             "0",
				"currency":          "CNY",
				"notes":             "",
				"pinned":            "0",
				"hidden":            "0",
			},
		})
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
	item, errMsg, err := parseDomainForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errMsg != "" {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "domain",
			Title:      "新增域名",
			Action:     "/domain/new",
			Error:      errMsg,
			Item:       item,
		})
		return
	}
	now := time.Now().In(time.Local).Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	sortOrder, err := nextSortOrder(ctx, a.db, "domains")
	if err != nil {
		a.serverError(w, err)
		return
	}
	_, err = a.db.ExecContext(ctx, insertDomainSQL, item["domain"], item["registrar"], item["expires_at"], item["price_f"], item["currency"], item["notes"], item["exclude_cost"], item["pinned"], item["hidden"], sortOrder, now, now)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleDomainEdit(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()
	id, err := parseIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		item, err := a.loadDomainItem(r.Context(), id)
		if err != nil {
			a.serverError(w, err)
			return
		}
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "domain",
			Title:      "编辑域名",
			Action:     "/domain/edit?id=" + strconv.FormatInt(id, 10),
			Item:       item,
		})
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
	item, errMsg, err := parseDomainForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errMsg != "" {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "domain",
			Title:      "编辑域名",
			Action:     "/domain/edit?id=" + strconv.FormatInt(id, 10),
			Error:      errMsg,
			Item:       item,
		})
		return
	}
	now := time.Now().In(time.Local).Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, err = a.db.ExecContext(ctx, `UPDATE domains SET domain=?, registrar=?, expires_at=?, renewal_price=?, currency=?, notes=?, exclude_cost=?, pinned=?, hidden=?, updated_at=? WHERE id=?`,
		item["domain"], item["registrar"], item["expires_at"], item["price_f"], item["currency"], item["notes"], item["exclude_cost"], item["pinned"], item["hidden"], now, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleDomainDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := parseIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := a.db.ExecContext(ctx, `DELETE FROM domains WHERE id=?`, id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleDomainRenew(w http.ResponseWriter, r *http.Request) {
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
	id, err := parseIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	expiresAt, err := parseRenewDate(r.FormValue("expires_at"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := time.Now().In(time.Local).Format(time.RFC3339)
	if _, err := a.db.ExecContext(ctx, `UPDATE domains SET expires_at=?, updated_at=? WHERE id=?`, expiresAt, now, id); err != nil {
		a.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) handleDomainPin(w http.ResponseWriter, r *http.Request) {
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
	id, err := parseIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pinned := 0
	if r.FormValue("pinned") == "1" {
		pinned = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := time.Now().In(time.Local).Format(time.RFC3339)
	if _, err := a.db.ExecContext(ctx, `UPDATE domains SET pinned=?, updated_at=? WHERE id=?`, pinned, now, id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleDomainBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	req, err := decodeBatchRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	now := time.Now().In(time.Local).Format(time.RFC3339)
	placeholders, args := buildInClause(req.IDs)
	if placeholders == "" {
		http.Error(w, "empty ids", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "delete":
		query := "DELETE FROM domains WHERE id IN " + placeholders
		if _, err := a.db.ExecContext(ctx, query, args...); err != nil {
			a.serverError(w, err)
			return
		}
	case "renew":
		expiresAt, err := parseRenewDate(req.Value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		query := "UPDATE domains SET expires_at=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{expiresAt, now}, args...)
		if _, err := a.db.ExecContext(ctx, query, params...); err != nil {
			a.serverError(w, err)
			return
		}
	case "pin", "unpin":
		val := 0
		if req.Action == "pin" {
			val = 1
		}
		query := "UPDATE domains SET pinned=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{val, now}, args...)
		if _, err := a.db.ExecContext(ctx, query, params...); err != nil {
			a.serverError(w, err)
			return
		}
	case "exclude_cost", "include_cost":
		val := 0
		if req.Action == "exclude_cost" {
			val = 1
		}
		query := "UPDATE domains SET exclude_cost=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{val, now}, args...)
		if _, err := a.db.ExecContext(ctx, query, params...); err != nil {
			a.serverError(w, err)
			return
		}
	case "hide", "unhide":
		val := 0
		if req.Action == "hide" {
			val = 1
		}
		query := "UPDATE domains SET hidden=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{val, now}, args...)
		if _, err := a.db.ExecContext(ctx, query, params...); err != nil {
			a.serverError(w, err)
			return
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
func (a *app) handleDomainExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filter, err := parseDomainExportFilter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "domain"
	}
	switch mode {
	case "domain", "domains", "only", "plain", "txt":
		if err := a.exportDomainsPlain(w, r.Context(), filter); err != nil {
			a.serverError(w, err)
		}
	case "detail", "details", "full", "csv":
		if err := a.exportDomainsCSV(w, r.Context(), filter); err != nil {
			a.serverError(w, err)
		}
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
	}
}
func parseDomainExportFilter(r *http.Request) (domainExportFilter, error) {
	q := r.URL.Query()
	scope := strings.ToLower(strings.TrimSpace(q.Get("scope")))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "filter", "page":
	default:
		return domainExportFilter{}, errors.New("invalid scope")
	}
	filter := domainExportFilter{Scope: scope}
	if scope == "filter" || scope == "page" {
		filter.Domain = strings.TrimSpace(q.Get("f_domain"))
		filter.Registrar = strings.TrimSpace(q.Get("f_registrar"))
		filter.Notes = strings.TrimSpace(q.Get("f_notes"))
	}
	if scope == "page" {
		page := 1
		pageStr := strings.TrimSpace(q.Get("page"))
		if pageStr != "" {
			p, err := strconv.Atoi(pageStr)
			if err != nil || p <= 0 {
				return domainExportFilter{}, errors.New("invalid page")
			}
			page = p
		}
		pageSize := 24
		sizeStr := strings.TrimSpace(q.Get("page_size"))
		if sizeStr != "" {
			s, err := strconv.Atoi(sizeStr)
			if err != nil || s < 0 {
				return domainExportFilter{}, errors.New("invalid page size")
			}
			pageSize = s
		}
		filter.Page = page
		filter.PageSize = pageSize
	}
	return filter, nil
}
func buildDomainExportQuery(base string, filter domainExportFilter) (string, []interface{}) {
	query := base
	var where []string
	var args []interface{}
	if filter.Scope == "filter" || filter.Scope == "page" {
		if filter.Domain != "" {
			where = append(where, "lower(domain) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+strings.ToLower(escapeLike(filter.Domain))+"%")
		}
		if filter.Registrar != "" {
			where = append(where, "lower(registrar) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+strings.ToLower(escapeLike(filter.Registrar))+"%")
		}
		if filter.Notes != "" {
			where = append(where, "lower(notes) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+strings.ToLower(escapeLike(filter.Notes))+"%")
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY pinned DESC, (sort_order = 0) ASC, sort_order ASC, expires_at ASC, id ASC"
	if filter.Scope == "page" && filter.PageSize > 0 {
		offset := (filter.Page - 1) * filter.PageSize
		if offset < 0 {
			offset = 0
		}
		query += " LIMIT ? OFFSET ?"
		args = append(args, filter.PageSize, offset)
	}
	return query, args
}
func escapeLike(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
func (a *app) exportDomainsPlain(w http.ResponseWriter, ctx context.Context, filter domainExportFilter) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query, args := buildDomainExportQuery(`SELECT domain FROM domains`, filter)
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return err
		}
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		lines = append(lines, domain)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	filename := "mb-domains-" + time.Now().In(time.Local).Format("20060102-150405") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, strings.Join(lines, "\n"))
	return nil
}
func (a *app) exportDomainsCSV(w http.ResponseWriter, ctx context.Context, filter domainExportFilter) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query, args := buildDomainExportQuery(`SELECT domain, registrar, expires_at, renewal_price, currency, notes, exclude_cost, pinned, hidden FROM domains`, filter)
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var buf bytes.Buffer
	buf.WriteString("\uFEFF")
	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{"domain", "registrar", "expires_at", "renewal_price", "currency", "notes", "exclude_cost", "pinned", "hidden"}); err != nil {
		return err
	}

	for rows.Next() {
		var domain, registrar, expiresAt, currency, notes string
		var price float64
		var excludeCost, pinned, hidden int
		if err := rows.Scan(&domain, &registrar, &expiresAt, &price, &currency, &notes, &excludeCost, &pinned, &hidden); err != nil {
			return err
		}
		expiresAt = normalizeISODateOrPermanent(expiresAt)
		if isPermanentISODate(expiresAt) {
			expiresAt = "永久"
		}
		record := []string{
			strings.TrimSpace(domain),
			strings.TrimSpace(registrar),
			expiresAt,
			formatFloat(price),
			strings.TrimSpace(currency),
			notes,
			boolTo01(excludeCost != 0),
			boolTo01(pinned != 0),
			boolTo01(hidden != 0),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}

	filename := "mb-domains-" + time.Now().In(time.Local).Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
	return nil
}
func domainSuffixKey(domain string) (string, string) {
	cleaned := strings.TrimSpace(domain)
	cleaned = strings.Trim(cleaned, ".")
	lower := strings.ToLower(cleaned)
	if lower == "" {
		return "", ""
	}
	parts := strings.Split(lower, ".")
	suffix := parts[len(parts)-1]
	return suffix, lower
}
func parseDomainForm(r *http.Request) (map[string]string, string, error) {
	domain := strings.TrimSpace(r.FormValue("domain"))
	registrar := strings.TrimSpace(r.FormValue("registrar"))
	expiresAt := strings.TrimSpace(r.FormValue("expires_at"))
	expiresPermanent := r.FormValue("expires_permanent") == "1"
	priceStr := strings.TrimSpace(r.FormValue("price"))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	notes := strings.TrimSpace(r.FormValue("notes"))
	excludeCost := r.FormValue("exclude_cost") == "1"
	pinned := r.FormValue("pinned") == "1"
	hidden := r.FormValue("hidden") == "1"

	expiresAt = normalizeISODateOrPermanent(expiresAt)
	if expiresPermanent || isPermanentISODate(expiresAt) {
		expiresAt = permanentISODate
	}

	item := map[string]string{
		"domain":            domain,
		"registrar":         registrar,
		"expires_at":        expiresAt,
		"expires_permanent": boolTo01(expiresPermanent || isPermanentISODate(expiresAt)),
		"price":             priceStr,
		"currency":          currency,
		"notes":             notes,
		"exclude_cost":      boolTo01(excludeCost),
		"pinned":            boolTo01(pinned),
		"hidden":            boolTo01(hidden),
	}

	if domain == "" {
		return item, "域名不能为空", nil
	}
	if !isPermanentISODate(expiresAt) {
		if _, err := parseISODate(expiresAt); err != nil {
			return item, "到期时间格式应为 YYYY-MM-DD 或 选择永久", nil
		}
	}
	price, err := parseMoney(priceStr)
	if err != nil {
		return item, "续费价格格式不正确", nil
	}
	if !isSupportedCurrency(currency) {
		return item, "币种仅支持 CNY/USD/EUR/RUB", nil
	}
	item["price_f"] = strconv.FormatFloat(price, 'f', -1, 64)
	return item, "", nil
}
func normalizeDomainURL(domain string) string {
	raw := strings.TrimSpace(domain)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}
