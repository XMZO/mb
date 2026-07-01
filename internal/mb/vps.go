package mb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *app) loadVPS(ctx context.Context, fx fxInfo, baseCurrency string) ([]vpsRow, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := a.db.QueryContext(ctx, `SELECT id, vendor, ip, specs, region, cores, memory_gb, disk_gb, disk_type, disk2_gb, disk2_type, disks_json, traffic_tb, traffic_set, traffic_direction, bandwidth_value, bandwidth_unit, expires_at, renewal_price, currency, notes, exclude_cost, billing_months, pinned, hidden FROM vps
		ORDER BY pinned DESC, (sort_order = 0) ASC, sort_order ASC, expires_at ASC, id ASC`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []vpsRow
	var total float64
	for rows.Next() {
		var r vpsRow
		var disksJSON string
		var trafficSet int
		var excludeCost int
		var billingMonths int
		var pinned int
		var hidden int
		if err := rows.Scan(&r.ID, &r.Vendor, &r.IP, &r.Specs, &r.Region, &r.Cores, &r.MemoryGB, &r.DiskGB, &r.DiskType, &r.Disk2GB, &r.Disk2Type, &disksJSON, &r.TrafficTB, &trafficSet, &r.TrafficDir, &r.BandwidthValue, &r.BandwidthUnit, &r.ExpiresAt, &r.RenewalPrice, &r.Currency, &r.Notes, &excludeCost, &billingMonths, &pinned, &hidden); err != nil {
			return nil, 0, err
		}
		r.ExcludeCost = excludeCost != 0
		r.Pinned = pinned != 0
		r.Hidden = hidden != 0
		if billingMonths <= 0 {
			billingMonths = 12
		}
		r.BillingMonths = billingMonths
		r.BillingLabel = billingLabel(billingMonths)
		r.DiskType = normalizeDiskType(r.DiskType)
		r.Disk2Type = normalizeDiskType(r.Disk2Type)
		r.Disks = disksFromStorage(disksJSON, r.DiskGB, r.DiskType, r.Disk2GB, r.Disk2Type)
		r.TrafficSet = trafficSet != 0
		if r.TrafficSet {
			r.TrafficDir = normalizeTrafficDir(r.TrafficDir)
		} else {
			r.TrafficDir = ""
		}
		r.BandwidthUnit = normalizeBandwidthUnit(r.BandwidthUnit)
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
		annualPrice := annualizePrice(r.RenewalPrice, billingMonths)
		r.AnnualPriceBase = convertMoney(annualPrice, r.Currency, baseCurrency, fx)
		if r.ExcludeCost {
			r.AnnualPriceBase = 0
		}
		total += r.AnnualPriceBase
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
func (a *app) loadVPSItem(ctx context.Context, id int64) (map[string]string, []diskFormRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var (
		vendor        string
		ip            string
		specs         string
		region        string
		cores         int
		memoryGB      float64
		diskGB        int
		diskType      string
		disk2GB       int
		disk2Type     string
		disksJSON     string
		trafficTB     float64
		trafficSet    int
		trafficDir    string
		bandwidthVal  float64
		bandwidthUnit string
		expiresAt     string
		price         float64
		currency      string
		notes         string
		excludeCost   int
		billingMonths int
		pinned        int
		hidden        int
	)
	err := a.db.QueryRowContext(ctx, `SELECT vendor, ip, specs, region, cores, memory_gb, disk_gb, disk_type, disk2_gb, disk2_type, disks_json, traffic_tb, traffic_set, traffic_direction, bandwidth_value, bandwidth_unit, expires_at, renewal_price, currency, notes, exclude_cost, billing_months, pinned, hidden FROM vps WHERE id=?`, id).
		Scan(&vendor, &ip, &specs, &region, &cores, &memoryGB, &diskGB, &diskType, &disk2GB, &disk2Type, &disksJSON, &trafficTB, &trafficSet, &trafficDir, &bandwidthVal, &bandwidthUnit, &expiresAt, &price, &currency, &notes, &excludeCost, &billingMonths, &pinned, &hidden)
	if err != nil {
		return nil, nil, err
	}
	diskType = normalizeDiskType(diskType)
	disk2Type = normalizeDiskType(disk2Type)
	diskRows := diskFormRowsFromSpecs(disksFromStorage(disksJSON, diskGB, diskType, disk2GB, disk2Type))
	expiresAt = normalizeISODateOrPermanent(expiresAt)

	coresStr := ""
	if cores > 0 {
		coresStr = strconv.Itoa(cores)
	}
	memoryStr := ""
	if memoryGB > 0 {
		memoryStr = formatFloat(memoryGB)
	}
	trafficStr := ""
	if trafficSet != 0 {
		trafficStr = formatFloat(trafficTB)
	}
	trafficDir = normalizeTrafficDir(trafficDir)
	if trafficSet == 0 {
		trafficDir = ""
	}
	bandwidthStr := ""
	if bandwidthVal > 0 {
		bandwidthStr = formatFloat(bandwidthVal)
	}
	bandwidthUnit = normalizeBandwidthUnit(bandwidthUnit)
	if billingMonths <= 0 {
		billingMonths = 12
	}
	return map[string]string{
		"vendor":            vendor,
		"ip":                ip,
		"specs":             specs,
		"region":            region,
		"cores":             coresStr,
		"memory_gb":         memoryStr,
		"traffic_tb":        trafficStr,
		"traffic_dir":       trafficDir,
		"bandwidth_value":   bandwidthStr,
		"bandwidth_unit":    bandwidthUnit,
		"expires_at":        expiresAt,
		"expires_permanent": boolTo01(isPermanentISODate(expiresAt)),
		"price":             formatFloat(price),
		"currency":          currency,
		"notes":             notes,
		"exclude_cost":      boolTo01(excludeCost != 0),
		"billing_months":    strconv.Itoa(billingMonths),
		"pinned":            boolTo01(pinned != 0),
		"hidden":            boolTo01(hidden != 0),
	}, diskRows, nil
}
func (a *app) handleVPSNew(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()
	if r.Method == http.MethodGet {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "vps",
			Title:      "新增 VPS",
			Action:     "/vps/new",
			Item: map[string]string{
				"vendor":            "",
				"ip":                "",
				"region":            "",
				"cores":             "",
				"memory_gb":         "",
				"traffic_tb":        "",
				"traffic_dir":       "",
				"bandwidth_value":   "",
				"bandwidth_unit":    "Mbps",
				"specs":             "",
				"expires_at":        time.Now().In(time.Local).AddDate(1, 0, 0).Format("2006-01-02"),
				"expires_permanent": "0",
				"price":             "0",
				"currency":          "CNY",
				"notes":             "",
				"pinned":            "0",
				"hidden":            "0",
			},
			DiskRows: []diskFormRow{{}},
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
	item, diskRows, errMsg, err := parseVPSForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errMsg != "" {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "vps",
			Title:      "新增 VPS",
			Action:     "/vps/new",
			Error:      errMsg,
			Item:       item,
			DiskRows:   diskRows,
		})
		return
	}
	now := time.Now().In(time.Local).Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	sortOrder, err := nextSortOrder(ctx, a.db, "vps")
	if err != nil {
		a.serverError(w, err)
		return
	}
	_, err = a.db.ExecContext(ctx, insertVPSSQL,
		item["vendor"], item["ip"], item["specs"], item["region"], item["cores_i"], item["memory_gb_f"], item["disk_gb_i"], item["disk_type"], item["disk2_gb_i"], item["disk2_type"], item["disks_json"], item["traffic_tb_f"], item["traffic_set_i"], item["traffic_dir"], item["bandwidth_value_f"], item["bandwidth_unit"], item["bandwidth_mbps_i"], item["expires_at"], item["price_f"], item["currency"], item["notes"], item["exclude_cost"], item["billing_months_i"], item["pinned"], item["hidden"], sortOrder, now, now)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleVPSEdit(w http.ResponseWriter, r *http.Request) {
	publicMode := mustGetBoolSetting(a.db, settingPublicMode, false)
	favicon := a.getFaviconURL()
	id, err := parseIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		item, diskRows, err := a.loadVPSItem(r.Context(), id)
		if err != nil {
			a.serverError(w, err)
			return
		}
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "vps",
			Title:      "编辑 VPS",
			Action:     "/vps/edit?id=" + strconv.FormatInt(id, 10),
			Item:       item,
			DiskRows:   diskRows,
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
	item, diskRows, errMsg, err := parseVPSForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errMsg != "" {
		a.render(w, "asset_form", assetFormVM{
			IsAdmin:    true,
			PublicMode: publicMode,
			FaviconURL: favicon,
			Kind:       "vps",
			Title:      "编辑 VPS",
			Action:     "/vps/edit?id=" + strconv.FormatInt(id, 10),
			Error:      errMsg,
			Item:       item,
			DiskRows:   diskRows,
		})
		return
	}
	now := time.Now().In(time.Local).Format(time.RFC3339)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, err = a.db.ExecContext(ctx, `UPDATE vps SET vendor=?, ip=?, specs=?, region=?, cores=?, memory_gb=?, disk_gb=?, disk_type=?, disk2_gb=?, disk2_type=?, disks_json=?, traffic_tb=?, traffic_set=?, traffic_direction=?, bandwidth_value=?, bandwidth_unit=?, bandwidth_mbps=?, expires_at=?, renewal_price=?, currency=?, notes=?, exclude_cost=?, billing_months=?, pinned=?, hidden=?, updated_at=? WHERE id=?`,
		item["vendor"], item["ip"], item["specs"], item["region"], item["cores_i"], item["memory_gb_f"], item["disk_gb_i"], item["disk_type"], item["disk2_gb_i"], item["disk2_type"], item["disks_json"], item["traffic_tb_f"], item["traffic_set_i"], item["traffic_dir"], item["bandwidth_value_f"], item["bandwidth_unit"], item["bandwidth_mbps_i"], item["expires_at"], item["price_f"], item["currency"], item["notes"], item["exclude_cost"], item["billing_months_i"], item["pinned"], item["hidden"], now, id)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleVPSDelete(w http.ResponseWriter, r *http.Request) {
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
	if _, err := a.db.ExecContext(ctx, `DELETE FROM vps WHERE id=?`, id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleVPSRenew(w http.ResponseWriter, r *http.Request) {
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
	if _, err := a.db.ExecContext(ctx, `UPDATE vps SET expires_at=?, updated_at=? WHERE id=?`, expiresAt, now, id); err != nil {
		a.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) handleVPSPin(w http.ResponseWriter, r *http.Request) {
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
	if _, err := a.db.ExecContext(ctx, `UPDATE vps SET pinned=?, updated_at=? WHERE id=?`, pinned, now, id); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) handleVPSBatch(w http.ResponseWriter, r *http.Request) {
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
		query := "DELETE FROM vps WHERE id IN " + placeholders
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
		query := "UPDATE vps SET expires_at=?, updated_at=? WHERE id IN " + placeholders
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
		query := "UPDATE vps SET pinned=?, updated_at=? WHERE id IN " + placeholders
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
		query := "UPDATE vps SET exclude_cost=?, updated_at=? WHERE id IN " + placeholders
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
		query := "UPDATE vps SET hidden=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{val, now}, args...)
		if _, err := a.db.ExecContext(ctx, query, params...); err != nil {
			a.serverError(w, err)
			return
		}
	case "billing_months":
		raw := strings.TrimSpace(req.Value)
		months, err := strconv.Atoi(raw)
		if err != nil || months <= 0 {
			http.Error(w, "付费周期应为正整数月数", http.StatusBadRequest)
			return
		}
		query := "UPDATE vps SET billing_months=?, updated_at=? WHERE id IN " + placeholders
		params := append([]interface{}{months, now}, args...)
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
func parseVPSForm(r *http.Request) (map[string]string, []diskFormRow, string, error) {
	vendor := strings.TrimSpace(r.FormValue("vendor"))
	region := strings.TrimSpace(r.FormValue("region"))
	ip := strings.TrimSpace(r.FormValue("ip"))
	coresStr := strings.TrimSpace(r.FormValue("cores"))
	memoryStr := strings.TrimSpace(r.FormValue("memory_gb"))
	trafficStr := strings.TrimSpace(r.FormValue("traffic_tb"))
	trafficDir := strings.TrimSpace(r.FormValue("traffic_dir"))
	bandwidthStr := strings.TrimSpace(r.FormValue("bandwidth_value"))
	bandwidthUnit := strings.TrimSpace(r.FormValue("bandwidth_unit"))
	specs := strings.TrimSpace(r.FormValue("specs"))
	expiresAt := strings.TrimSpace(r.FormValue("expires_at"))
	expiresPermanent := r.FormValue("expires_permanent") == "1"
	priceStr := strings.TrimSpace(r.FormValue("price"))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	notes := strings.TrimSpace(r.FormValue("notes"))
	billingRaw := strings.TrimSpace(r.FormValue("billing_months"))
	excludeCost := r.FormValue("exclude_cost") == "1"
	pinned := r.FormValue("pinned") == "1"
	hidden := r.FormValue("hidden") == "1"

	bandwidthUnit = normalizeBandwidthUnit(bandwidthUnit)
	expiresAt = normalizeISODateOrPermanent(expiresAt)
	if expiresPermanent || isPermanentISODate(expiresAt) {
		expiresAt = permanentISODate
	}

	diskRows := parseDiskFormRows(r.Form["disk_gb"], r.Form["disk_type"])

	item := map[string]string{
		"vendor":            vendor,
		"region":            region,
		"ip":                ip,
		"cores":             coresStr,
		"memory_gb":         memoryStr,
		"traffic_tb":        trafficStr,
		"traffic_dir":       trafficDir,
		"bandwidth_value":   bandwidthStr,
		"bandwidth_unit":    bandwidthUnit,
		"specs":             specs,
		"expires_at":        expiresAt,
		"expires_permanent": boolTo01(expiresPermanent || isPermanentISODate(expiresAt)),
		"price":             priceStr,
		"currency":          currency,
		"notes":             notes,
		"billing_months":    billingRaw,
		"exclude_cost":      boolTo01(excludeCost),
		"pinned":            boolTo01(pinned),
		"hidden":            boolTo01(hidden),
	}

	if vendor == "" {
		return item, diskRows, "厂商不能为空", nil
	}
	if !isPermanentISODate(expiresAt) {
		if _, err := parseISODate(expiresAt); err != nil {
			return item, diskRows, "到期时间格式应为 YYYY-MM-DD 或 选择永久", nil
		}
	}

	parseOptionalInt := func(s string) (int, error) {
		if strings.TrimSpace(s) == "" {
			return 0, nil
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			return 0, err
		}
		if i < 0 {
			return 0, errors.New("negative")
		}
		return i, nil
	}
	parseOptionalFloat := func(s string) (float64, error) {
		if strings.TrimSpace(s) == "" {
			return 0, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		if !isFiniteNonNegative(f) {
			return 0, errors.New("invalid")
		}
		return f, nil
	}

	cores, err := parseOptionalInt(coresStr)
	if err != nil {
		return item, diskRows, "核心格式不正确", nil
	}
	memGB, err := parseOptionalFloat(memoryStr)
	if err != nil {
		return item, diskRows, "内存格式不正确", nil
	}

	diskSpecs, diskErrMsg := validateDiskRows(diskRows, 16)
	if diskErrMsg != "" {
		return item, diskRows, diskErrMsg, nil
	}

	disksJSON := ""
	if len(diskSpecs) > 0 {
		b, err := json.Marshal(diskSpecs)
		if err != nil {
			return item, diskRows, "", err
		}
		disksJSON = string(b)
	}

	trafficSet := 0
	var trafficTB float64
	if trafficStr != "" {
		trafficSet = 1
		trafficTB, err = parseOptionalFloat(trafficStr)
		if err != nil {
			return item, diskRows, "流量格式不正确", nil
		}
	}
	trafficDir = normalizeTrafficDir(trafficDir)
	if item["traffic_dir"] != "" && trafficDir == "" {
		return item, diskRows, "单双向流量选项不正确", nil
	}
	if trafficSet == 0 && trafficDir != "" {
		return item, diskRows, "流量留空则不显示；若需标注单双向，请填写流量（0=不限）", nil
	}

	bwVal, err := parseOptionalFloat(bandwidthStr)
	if err != nil {
		return item, diskRows, "带宽格式不正确", nil
	}
	bwUnit := normalizeBandwidthUnit(bandwidthUnit)
	if bandwidthUnit != "" && !strings.EqualFold(bandwidthUnit, "Mbps") && !strings.EqualFold(bandwidthUnit, "Gbps") {
		return item, diskRows, "带宽单位仅支持 Mbps/Gbps", nil
	}
	bwMbps := 0
	if bwVal > 0 {
		if bwUnit == "Gbps" {
			bwMbps = int(math.Round(bwVal * 1000))
		} else {
			bwMbps = int(math.Round(bwVal))
		}
	}

	price, err := parseMoney(priceStr)
	if err != nil {
		return item, diskRows, "续费价格格式不正确", nil
	}
	if !isSupportedCurrency(currency) {
		return item, diskRows, "币种仅支持 CNY/USD/EUR/RUB", nil
	}

	billingMonths, err := normalizeBillingMonths(billingRaw, 12)
	if err != nil {
		return item, diskRows, "付费周期格式不正确", nil
	}

	diskGB := 0
	diskType := ""
	disk2GB := 0
	disk2Type := ""
	if len(diskSpecs) >= 1 {
		diskGB = diskSpecs[0].SizeGB
		diskType = diskSpecs[0].Type
	}
	if len(diskSpecs) >= 2 {
		disk2GB = diskSpecs[1].SizeGB
		disk2Type = diskSpecs[1].Type
	}

	item["cores_i"] = strconv.Itoa(cores)
	item["memory_gb_f"] = strconv.FormatFloat(memGB, 'f', -1, 64)
	item["disk_gb_i"] = strconv.Itoa(diskGB)
	item["disk_type"] = diskType
	item["disk2_gb_i"] = strconv.Itoa(disk2GB)
	item["disk2_type"] = disk2Type
	item["disks_json"] = disksJSON
	item["traffic_tb_f"] = strconv.FormatFloat(trafficTB, 'f', -1, 64)
	item["traffic_set_i"] = strconv.Itoa(trafficSet)
	item["traffic_dir"] = trafficDir
	item["bandwidth_value_f"] = strconv.FormatFloat(bwVal, 'f', -1, 64)
	item["bandwidth_unit"] = bwUnit
	item["bandwidth_mbps_i"] = strconv.Itoa(bwMbps)
	item["price_f"] = strconv.FormatFloat(price, 'f', -1, 64)
	item["billing_months_i"] = strconv.Itoa(billingMonths)
	return item, diskRows, "", nil
}
func normalizeDiskType(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "SSD":
		return "SSD"
	case "HDD":
		return "HDD"
	default:
		return ""
	}
}
func parseDiskFormRows(sizes []string, types []string) []diskFormRow {
	count := len(sizes)
	if len(types) > count {
		count = len(types)
	}
	if count == 0 {
		return []diskFormRow{{}}
	}
	rows := make([]diskFormRow, 0, count)
	lastNonEmpty := -1
	for i := 0; i < count; i++ {
		size := ""
		if i < len(sizes) {
			size = strings.TrimSpace(sizes[i])
		}
		t := ""
		if i < len(types) {
			t = strings.ToUpper(strings.TrimSpace(types[i]))
		}
		rows = append(rows, diskFormRow{SizeGB: size, Type: t})
		if size != "" || t != "" {
			lastNonEmpty = i
		}
	}
	if lastNonEmpty == -1 {
		return []diskFormRow{{}}
	}
	return rows[:lastNonEmpty+1]
}
func validateDiskRows(rows []diskFormRow, maxDisks int) ([]diskSpec, string) {
	var disks []diskSpec
	for i, row := range rows {
		sizeStr := strings.TrimSpace(row.SizeGB)
		tRaw := strings.TrimSpace(row.Type)
		if sizeStr == "" && tRaw == "" {
			continue
		}
		if sizeStr == "" && tRaw != "" {
			return nil, fmt.Sprintf("第 %d 块硬盘已选择类型但未填写容量", i+1)
		}
		sizeGB, err := strconv.Atoi(sizeStr)
		if err != nil || sizeGB <= 0 {
			return nil, fmt.Sprintf("第 %d 块硬盘容量格式不正确", i+1)
		}
		t := normalizeDiskType(tRaw)
		if tRaw != "" && t == "" {
			return nil, fmt.Sprintf("第 %d 块硬盘类型仅支持 SSD/HDD", i+1)
		}
		disks = append(disks, diskSpec{SizeGB: sizeGB, Type: t})
		if maxDisks > 0 && len(disks) > maxDisks {
			return nil, fmt.Sprintf("最多支持 %d 块硬盘", maxDisks)
		}
	}
	return disks, ""
}
func disksFromStorage(disksJSON string, diskGB int, diskType string, disk2GB int, disk2Type string) []diskSpec {
	if strings.TrimSpace(disksJSON) != "" {
		var disks []diskSpec
		if err := json.Unmarshal([]byte(disksJSON), &disks); err == nil {
			var cleaned []diskSpec
			for _, d := range disks {
				if d.SizeGB <= 0 {
					continue
				}
				cleaned = append(cleaned, diskSpec{SizeGB: d.SizeGB, Type: normalizeDiskType(d.Type)})
			}
			return cleaned
		}
	}
	var out []diskSpec
	if diskGB > 0 {
		out = append(out, diskSpec{SizeGB: diskGB, Type: normalizeDiskType(diskType)})
	}
	if disk2GB > 0 {
		out = append(out, diskSpec{SizeGB: disk2GB, Type: normalizeDiskType(disk2Type)})
	}
	return out
}
func diskFormRowsFromSpecs(disks []diskSpec) []diskFormRow {
	if len(disks) == 0 {
		return []diskFormRow{{}}
	}
	rows := make([]diskFormRow, 0, len(disks))
	for _, d := range disks {
		size := ""
		if d.SizeGB > 0 {
			size = strconv.Itoa(d.SizeGB)
		}
		rows = append(rows, diskFormRow{SizeGB: size, Type: normalizeDiskType(d.Type)})
	}
	return rows
}
func normalizeBandwidthUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "", "mbps":
		return "Mbps"
	case "gbps":
		return "Gbps"
	default:
		return "Mbps"
	}
}
func normalizeTrafficDir(dir string) string {
	d := strings.TrimSpace(dir)
	if d == "" {
		return ""
	}
	switch strings.ToLower(d) {
	case "single":
		return "single"
	case "double":
		return "double"
	}
	switch d {
	case "单向":
		return "single"
	case "双向":
		return "double"
	default:
		return ""
	}
}
func normalizeBillingMonths(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		if fallback <= 0 {
			return 12, nil
		}
		return fallback, nil
	}
	m, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || m <= 0 {
		return 0, errors.New("invalid billing months")
	}
	return m, nil
}
func billingLabel(months int) string {
	switch months {
	case 1:
		return "月付"
	case 3:
		return "季付"
	case 6:
		return "半年付"
	case 12:
		return "年付"
	case 24:
		return "两年付"
	default:
		if months > 0 {
			return fmt.Sprintf("%d 个月", months)
		}
		return ""
	}
}
func annualizePrice(price float64, months int) float64 {
	if !isFiniteNonNegative(price) || months <= 0 {
		return 0
	}
	return price * 12 / float64(months)
}
