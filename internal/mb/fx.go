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
	eurToUSD := mustGetFloatSetting(a.db, settingFxEURToUSD, defaultFxEURToUSD)
	eurToCNY := mustGetFloatSetting(a.db, settingFxEURToCNY, defaultFxEURToCNY)
	eurToRUB := mustGetFloatSetting(a.db, settingFxEURToRUB, defaultFxEURToRUB)
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
		UpdatedAt:     updatedAt,
		RateDate:      rateDate,
		AutoUpdate:    mustGetBoolSetting(a.db, settingFxAutoUpdate, true),
		Source:        source,
		LastAttemptAt: lastAttemptAt,
		LastError:     lastErr,
	}
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

	usd, cny, rub, rateDate, err := fetchFxFromECB(ctx)
	if err == nil && isFinitePositive(usd) && isFinitePositive(cny) && isFinitePositive(rub) {
		return storeFxRates(db, usd, cny, rub, rateDate, fxSourceECB)
	}
	if fallbackErr := updateFxFromOpenER(ctx, db); fallbackErr == nil {
		return nil
	} else {
		_ = setSetting(db, settingFxLastError, fallbackErr.Error())
		return fallbackErr
	}
}
func storeFxRates(db *sql.DB, usd, cny, rub float64, rateDate, source string) error {
	if err := setSetting(db, settingFxEURToUSD, strconv.FormatFloat(usd, 'f', -1, 64)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxEURToCNY, strconv.FormatFloat(cny, 'f', -1, 64)); err != nil {
		return err
	}
	if err := setSetting(db, settingFxEURToRUB, strconv.FormatFloat(rub, 'f', -1, 64)); err != nil {
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
func fetchFxFromECB(ctx context.Context) (float64, float64, float64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml", nil)
	if err != nil {
		return 0, 0, 0, "", err
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
		return 0, 0, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, 0, 0, "", fmt.Errorf("ECB HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, 0, "", err
	}

	var env ecbEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return 0, 0, 0, "", err
	}
	if len(env.Cube.Cubes) == 0 {
		return 0, 0, 0, "", errors.New("ECB response missing rates")
	}
	c := env.Cube.Cubes[0]
	var usd, cny, rub float64
	for _, r := range c.Rates {
		switch strings.ToUpper(strings.TrimSpace(r.Currency)) {
		case "USD":
			usd = r.Rate
		case "CNY":
			cny = r.Rate
		case "RUB":
			rub = r.Rate
		}
	}
	if !isFinitePositive(usd) || !isFinitePositive(cny) || !isFinitePositive(rub) {
		return 0, 0, 0, "", errors.New("ECB rates missing USD/CNY/RUB")
	}
	return usd, cny, rub, strings.TrimSpace(c.Time), nil
}
func updateFxFromOpenER(ctx context.Context, db *sql.DB) error {
	usd, cny, rub, rateDate, err := fetchFxFromOpenER(ctx)
	if err != nil {
		return err
	}
	return storeFxRates(db, usd, cny, rub, rateDate, fxSourceOpenER)
}
func fetchFxFromOpenER(ctx context.Context) (float64, float64, float64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://open.er-api.com/v6/latest/EUR", nil)
	if err != nil {
		return 0, 0, 0, "", err
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
		return 0, 0, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, 0, 0, "", fmt.Errorf("OpenER HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, 0, "", err
	}

	var respData openERResponse
	if err := json.Unmarshal(body, &respData); err != nil {
		return 0, 0, 0, "", err
	}
	if strings.ToLower(respData.Result) != "success" {
		msg := strings.TrimSpace(respData.ErrorType)
		if msg == "" {
			msg = "open.er-api.com response not success"
		}
		return 0, 0, 0, "", errors.New(msg)
	}
	if base := strings.ToUpper(strings.TrimSpace(respData.BaseCode)); base != "" && base != "EUR" {
		return 0, 0, 0, "", fmt.Errorf("open.er-api.com base %s", base)
	}
	if respData.Rates == nil {
		return 0, 0, 0, "", errors.New("open.er-api.com missing rates")
	}
	usd := respData.Rates["USD"]
	cny := respData.Rates["CNY"]
	rub := respData.Rates["RUB"]
	if !isFinitePositive(usd) || !isFinitePositive(cny) || !isFinitePositive(rub) {
		return 0, 0, 0, "", errors.New("open.er-api.com rates missing USD/CNY/RUB")
	}
	return usd, cny, rub, strings.TrimSpace(respData.TimeLastUpdateUTC), nil
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
