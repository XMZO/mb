package mb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (a *app) handleSortDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	ids, err := decodeSortIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.updateSortOrder(ctx, "domains", ids); err != nil {
		a.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) handleSortVPS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := limitBody(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	ids, err := decodeSortIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.updateSortOrder(ctx, "vps", ids); err != nil {
		a.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) handleSortDomainsAlpha(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id FROM domains ORDER BY lower(domain) ASC, id ASC`
	a.handleSortByQuery(w, r, "domains", query)
}
func (a *app) handleSortDomainsSuffix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := a.db.QueryContext(ctx, `SELECT id, domain FROM domains`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()

	type item struct {
		id     int64
		suffix string
		name   string
	}
	var items []item
	for rows.Next() {
		var id int64
		var domain string
		if err := rows.Scan(&id, &domain); err != nil {
			a.serverError(w, err)
			return
		}
		suffix, name := domainSuffixKey(domain)
		items = append(items, item{id: id, suffix: suffix, name: name})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].suffix != items[j].suffix {
			return items[i].suffix < items[j].suffix
		}
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].id < items[j].id
	})
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.id)
	}
	if err := a.updateSortOrder(ctx, "domains", ids); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleSortDomainsExpiry(w http.ResponseWriter, r *http.Request) {
	query := fmt.Sprintf("SELECT id FROM domains ORDER BY (expires_at = '%s') ASC, expires_at ASC, id ASC", permanentISODate)
	a.handleSortByQuery(w, r, "domains", query)
}
func (a *app) handleSortDomainsPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	fx := a.getFxInfo()
	baseCurrency := baseCurrencyFromRequest(r)
	rows, err := a.db.QueryContext(ctx, `SELECT id, domain, renewal_price, currency FROM domains`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()

	type item struct {
		id    int64
		base  float64
		name  string
		price float64
	}
	var items []item
	for rows.Next() {
		var id int64
		var domain string
		var price float64
		var currency string
		if err := rows.Scan(&id, &domain, &price, &currency); err != nil {
			a.serverError(w, err)
			return
		}
		base := convertMoney(price, currency, baseCurrency, fx)
		items = append(items, item{id: id, base: base, name: strings.ToLower(domain), price: price})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].base != items[j].base {
			return items[i].base > items[j].base
		}
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].id < items[j].id
	})
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.id)
	}
	if err := a.updateSortOrder(ctx, "domains", ids); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleSortDomainsRegistrar(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id FROM domains ORDER BY (registrar = '') ASC, lower(registrar) ASC, lower(domain) ASC, id ASC`
	a.handleSortByQuery(w, r, "domains", query)
}
func (a *app) handleSortVPSRegion(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id FROM vps ORDER BY (region = '') ASC, lower(region) ASC, lower(vendor) ASC, id ASC`
	a.handleSortByQuery(w, r, "vps", query)
}
func (a *app) handleSortVPSExpiry(w http.ResponseWriter, r *http.Request) {
	query := fmt.Sprintf("SELECT id FROM vps ORDER BY (expires_at = '%s') ASC, expires_at ASC, id ASC", permanentISODate)
	a.handleSortByQuery(w, r, "vps", query)
}
func (a *app) handleSortVPSVendor(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id FROM vps ORDER BY lower(vendor) ASC, id ASC`
	a.handleSortByQuery(w, r, "vps", query)
}
func (a *app) handleSortVPSPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	fx := a.getFxInfo()
	baseCurrency := baseCurrencyFromRequest(r)
	rows, err := a.db.QueryContext(ctx, `SELECT id, vendor, renewal_price, currency, billing_months FROM vps`)
	if err != nil {
		a.serverError(w, err)
		return
	}
	defer rows.Close()

	type item struct {
		id   int64
		base float64
		name string
	}
	var items []item
	for rows.Next() {
		var id int64
		var vendor string
		var price float64
		var currency string
		var months int
		if err := rows.Scan(&id, &vendor, &price, &currency, &months); err != nil {
			a.serverError(w, err)
			return
		}
		if months <= 0 {
			months = 12
		}
		annual := annualizePrice(price, months)
		base := convertMoney(annual, currency, baseCurrency, fx)
		items = append(items, item{id: id, base: base, name: strings.ToLower(vendor)})
	}
	if err := rows.Err(); err != nil {
		a.serverError(w, err)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].base != items[j].base {
			return items[i].base > items[j].base
		}
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].id < items[j].id
	})
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.id)
	}
	if err := a.updateSortOrder(ctx, "vps", ids); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func decodeSortIDs(r *http.Request) ([]int64, error) {
	var req sortRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, err
	}
	if len(req.IDs) == 0 {
		return nil, errors.New("empty sort list")
	}
	seen := make(map[int64]struct{}, len(req.IDs))
	ids := make([]int64, 0, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("empty sort list")
	}
	return ids, nil
}
func decodeBatchRequest(r *http.Request) (batchRequest, error) {
	var req batchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if len(req.IDs) == 0 {
		return req, errors.New("empty ids")
	}
	seen := make(map[int64]struct{}, len(req.IDs))
	ids := make([]int64, 0, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return req, errors.New("empty ids")
	}
	req.IDs = ids
	return req, nil
}
func buildInClause(ids []int64) (string, []interface{}) {
	if len(ids) == 0 {
		return "", nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	return "(" + strings.Join(placeholders, ",") + ")", args
}
func (a *app) updateSortOrder(ctx context.Context, table string, ids []int64) error {
	if table == "" || len(ids) == 0 {
		return errors.New("empty sort list")
	}
	allIDs, err := a.loadAllIDsForSort(ctx, table)
	if err != nil {
		return err
	}
	if len(allIDs) == 0 {
		return errors.New("empty sort list")
	}
	existing := make(map[int64]struct{}, len(allIDs))
	for _, id := range allIDs {
		existing[id] = struct{}{}
	}
	ordered := make([]int64, 0, len(allIDs))
	seen := make(map[int64]struct{}, len(allIDs))
	for _, id := range ids {
		if _, ok := existing[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	if len(ordered) == 0 {
		return errors.New("empty sort list")
	}
	if len(ordered) < len(allIDs) {
		for _, id := range allIDs {
			if _, ok := seen[id]; ok {
				continue
			}
			ordered = append(ordered, id)
		}
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf("UPDATE %s SET sort_order = ? WHERE id = ?", table))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for idx, id := range ordered {
		if _, err := stmt.ExecContext(ctx, idx+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *app) loadAllIDsForSort(ctx context.Context, table string) ([]int64, error) {
	if table == "" {
		return nil, errors.New("empty sort list")
	}
	query := fmt.Sprintf("SELECT id FROM %s ORDER BY pinned DESC, (sort_order = 0) ASC, sort_order ASC, id ASC", table)
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
func (a *app) handleSortByQuery(w http.ResponseWriter, r *http.Request, table, query string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if table == "" || query == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ids, err := a.fetchSortedIDs(ctx, query)
	if err != nil {
		a.serverError(w, err)
		return
	}
	if err := a.updateSortOrder(ctx, table, ids); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) fetchSortedIDs(ctx context.Context, query string) ([]int64, error) {
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
