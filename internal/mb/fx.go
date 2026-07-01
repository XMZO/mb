package mb

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *app) getFxInfo() fxInfo {
	rates := a.getFxRates()
	eurToUSD := rates["USD"]
	eurToCNY := rates["CNY"]
	eurToRUB := rates["RUB"]
	updatedAt, _, _ := getSetting(a.db, settingFxUpdatedAt)
	rateDate, _, _ := getSetting(a.db, settingFxRateDate)
	lastAttemptAt, _, _ := getSetting(a.db, settingFxLastAttempt)
	lastErr, _, _ := getSetting(a.db, settingFxLastError)
	source, _, _ := getSetting(a.db, settingFxSource)
	if source == "" {
		source = fxSourceManual
	}
	return fxInfo{
		EURToUSD:      eurToUSD,
		EURToCNY:      eurToCNY,
		EURToRUB:      eurToRUB,
		Rates:         rates,
		UpdatedAt:     updatedAt,
		RateDate:      rateDate,
		AutoUpdate:    mustGetBoolSetting(a.db, settingFxAutoUpdate, true),
		Source:        source,
		LastAttemptAt: lastAttemptAt,
		LastError:     lastErr,
	}
}
func (a *app) getFxRates() map[string]float64 {
	rates := defaultFxRates()
	if raw, ok, err := getSetting(a.db, settingFxRatesJSON); err == nil && ok {
		for code, rate := range parseFxRatesJSON(raw) {
			if isSupportedCurrency(code) && isFinitePositive(rate) {
				rates[code] = rate
			}
		}
	}

	legacy := map[string]string{
		"USD": settingFxEURToUSD,
		"CNY": settingFxEURToCNY,
		"RUB": settingFxEURToRUB,
	}
	for code, key := range legacy {
		if rate := mustGetFloatSetting(a.db, key, 0); isFinitePositive(rate) {
			rates[code] = rate
		}
	}
	rates["EUR"] = 1
	return rates
}
func defaultFxRates() map[string]float64 {
	return map[string]float64{
		"EUR": 1,
		"USD": defaultFxEURToUSD,
		"CNY": defaultFxEURToCNY,
		"RUB": defaultFxEURToRUB,
	}
}
func parseFxRatesJSON(raw string) map[string]float64 {
	var rates map[string]float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &rates); err != nil {
		return nil
	}
	out := make(map[string]float64, len(rates))
	for code, rate := range rates {
		code = strings.ToUpper(strings.TrimSpace(code))
		if isSupportedCurrency(code) && isFinitePositive(rate) {
			out[code] = rate
		}
	}
	return out
}
func (a *app) maybeAutoUpdateFx(fx fxInfo) {
	if fx.LastAttemptAt != "" {
		if t, err := time.Parse(time.RFC3339, fx.LastAttemptAt); err == nil {
			if time.Since(t) < 18*time.Hour {
				return
			}
		}
	}

	a.fxMu.Lock()
	if a.fxUpdating {
		a.fxMu.Unlock()
		return
	}
	a.fxUpdating = true
	a.fxMu.Unlock()

	go func() {
		defer func() {
			a.fxMu.Lock()
			a.fxUpdating = false
			a.fxMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = updateFxFromECB(ctx, a.db)
	}()
}
func updateFxFromECB(ctx context.Context, db *sql.DB) error {
	_ = setSetting(db, settingFxLastAttempt, time.Now().In(time.Local).Format(time.RFC3339))
	_ = setSetting(db, settingFxLastError, "")

	rates, rateDate, err := fetchFxFromECB(ctx)
	if err == nil && hasAllSupportedFxRates(rates) {
		return storeFxRates(db, rates, rateDate, fxSourceECB)
	}
	if fallbackErr := updateFxFromOpenER(ctx, db); fallbackErr == nil {
		return nil
	} else {
		_ = setSetting(db, settingFxLastError, fallbackErr.Error())
		return fallbackErr
	}
}
func storeFxRates(db *sql.DB, rates map[string]float64, rateDate, source string) error {
	normalized := normalizeFxRates(rates)
	if !hasCoreFxRates(normalized) {
		return errors.New("汇率缺少核心币种 USD/CNY/RUB")
	}
	ratesJSON, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	if err := setSetting(db, settingFxRatesJSON, string(ratesJSON)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxEURToUSD, strconv.FormatFloat(normalized["USD"], 'f', -1, 64)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxEURToCNY, strconv.FormatFloat(normalized["CNY"], 'f', -1, 64)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxEURToRUB, strconv.FormatFloat(normalized["RUB"], 'f', -1, 64)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxUpdatedAt, time.Now().In(time.Local).Format(time.RFC3339)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxRateDate, strings.TrimSpace(rateDate)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxSource, source); err != nil {
		return err
	}
	_ = setSetting(db, settingFxLastError, "")
	return nil
}
func normalizeFxRates(rates map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(rates)+1)
	out["EUR"] = 1
	for _, code := range supportedCurrencyCodes() {
		if code == "EUR" {
			continue
		}
		if rate := rates[code]; isFinitePositive(rate) {
			out[code] = rate
		}
	}
	return out
}
func hasCoreFxRates(rates map[string]float64) bool {
	for _, code := range []string{"USD", "CNY", "RUB"} {
		if !isFinitePositive(rates[code]) {
			return false
		}
	}
	return true
}
func hasAllSupportedFxRates(rates map[string]float64) bool {
	for _, code := range supportedCurrencyCodes() {
		if code == "EUR" {
			continue
		}
		if !isFinitePositive(rates[code]) {
			return false
		}
	}
	return true
}
func fetchFxFromECB(ctx context.Context) (map[string]float64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "mb/1.0")
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", fmt.Errorf("ECB HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}

	var env ecbEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, "", err
	}
	if len(env.Cube.Cubes) == 0 {
		return nil, "", errors.New("ECB response missing rates")
	}
	c := env.Cube.Cubes[0]
	rates := map[string]float64{"EUR": 1}
	for _, r := range c.Rates {
		code := strings.ToUpper(strings.TrimSpace(r.Currency))
		if isSupportedCurrency(code) && isFinitePositive(r.Rate) {
			rates[code] = r.Rate
		}
	}
	if !hasCoreFxRates(rates) {
		return nil, "", errors.New("ECB rates missing USD/CNY/RUB")
	}
	return rates, strings.TrimSpace(c.Time), nil
}
func updateFxFromOpenER(ctx context.Context, db *sql.DB) error {
	rates, rateDate, err := fetchFxFromOpenER(ctx)
	if err != nil {
		return err
	}
	return storeFxRates(db, rates, rateDate, fxSourceOpenER)
}
func fetchFxFromOpenER(ctx context.Context) (map[string]float64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://open.er-api.com/v6/latest/EUR", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "mb/1.0")
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", fmt.Errorf("OpenER HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}

	var respData openERResponse
	if err := json.Unmarshal(body, &respData); err != nil {
		return nil, "", err
	}
	if strings.ToLower(respData.Result) != "success" {
		msg := strings.TrimSpace(respData.ErrorType)
		if msg == "" {
			msg = "open.er-api.com response not success"
		}
		return nil, "", errors.New(msg)
	}
	if base := strings.ToUpper(strings.TrimSpace(respData.BaseCode)); base != "" && base != "EUR" {
		return nil, "", fmt.Errorf("open.er-api.com base %s", base)
	}
	if respData.Rates == nil {
		return nil, "", errors.New("open.er-api.com missing rates")
	}
	rates := map[string]float64{"EUR": 1}
	for _, code := range supportedCurrencyCodes() {
		if code == "EUR" {
			continue
		}
		if rate := respData.Rates[code]; isFinitePositive(rate) {
			rates[code] = rate
		}
	}
	if !hasCoreFxRates(rates) {
		return nil, "", errors.New("open.er-api.com rates missing USD/CNY/RUB")
	}
	return rates, strings.TrimSpace(respData.TimeLastUpdateUTC), nil
}
func (a *app) handleFxUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := updateFxFromECB(ctx, a.db); err != nil {
		a.settingsError(w, "拉取 ECB 汇率失败,请稍后重试或查看服务端日志", err)
		return
	}
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}
