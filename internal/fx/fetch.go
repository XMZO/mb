package fx

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mb/internal/money"
)

// 数据源地址;测试中会替换为本地服务。
var (
	openERURL = "https://open.er-api.com/v6/latest/EUR"
	ecbURL    = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
)

const maxResponseBytes = 1 << 20

// fetched 是一次成功拉取的结果,汇率以 EUR 为基准。
type fetched struct {
	rates  money.Rates
	date   string
	source string
}

// fetchOpenER 从 open.er-api.com 拉取汇率,覆盖约 160 个币种。
func fetchOpenER(ctx context.Context, client *http.Client) (fetched, error) {
	body, err := httpGet(ctx, client, openERURL)
	if err != nil {
		return fetched{}, fmt.Errorf("open.er-api.com: %w", err)
	}
	var data struct {
		Result            string             `json:"result"`
		BaseCode          string             `json:"base_code"`
		TimeLastUpdateUTC string             `json:"time_last_update_utc"`
		Rates             map[string]float64 `json:"rates"`
		ErrorType         string             `json:"error-type"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return fetched{}, fmt.Errorf("open.er-api.com: %w", err)
	}
	if !strings.EqualFold(data.Result, "success") {
		return fetched{}, fmt.Errorf("open.er-api.com: %s", orDefault(data.ErrorType, "request failed"))
	}
	if base := money.Normalize(data.BaseCode); base != "" && base != "EUR" {
		return fetched{}, fmt.Errorf("open.er-api.com: unexpected base currency %s", base)
	}
	return checked(data.Rates, shortDate(data.TimeLastUpdateUTC), SourceOpenER)
}

// shortDate 把 "Thu, 12 Nov 2026 00:02:31 +0000" 这样的时间戳简化为 "2026-11-12";无法解析时原样返回。
func shortDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC1123Z, raw); err == nil {
		return t.Format("2006-01-02")
	}
	return raw
}

// fetchECB 从欧洲央行拉取每日参考汇率,只覆盖约 30 个主要币种。
func fetchECB(ctx context.Context, client *http.Client) (fetched, error) {
	body, err := httpGet(ctx, client, ecbURL)
	if err != nil {
		return fetched{}, fmt.Errorf("ECB: %w", err)
	}
	var envelope struct {
		Days []struct {
			Date  string `xml:"time,attr"`
			Rates []struct {
				Currency string  `xml:"currency,attr"`
				Rate     float64 `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube>Cube"`
	}
	if err := xml.Unmarshal(body, &envelope); err != nil {
		return fetched{}, fmt.Errorf("ECB: %w", err)
	}
	if len(envelope.Days) == 0 {
		return fetched{}, errors.New("ECB: response contains no rates")
	}
	day := envelope.Days[0]
	rates := make(map[string]float64, len(day.Rates))
	for _, r := range day.Rates {
		rates[r.Currency] = r.Rate
	}
	return checked(rates, strings.TrimSpace(day.Date), SourceECB)
}

// checked 过滤掉不受支持或非法的条目,并确认最常用的币种都在,防止把残缺的数据存下来。
func checked(raw map[string]float64, date, source string) (fetched, error) {
	rates := make(money.Rates, len(raw))
	for code, rate := range raw {
		code = money.Normalize(code)
		if code != "EUR" && money.IsSupported(code) && money.Positive(rate) {
			rates[code] = rate
		}
	}
	for _, code := range []string{"USD", "CNY"} {
		if _, ok := rates[code]; !ok {
			return fetched{}, fmt.Errorf("%s: response is missing %s", source, code)
		}
	}
	return fetched{rates: rates, date: date, source: source}, nil
}

func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mb-asset-board")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

func orDefault(s, fallback string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return fallback
}
