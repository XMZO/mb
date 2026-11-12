// Package fx 管理汇率:从公开数据源拉取、保存到设置项,并叠加用户手动指定的汇率。
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"mb/internal/money"
	"mb/internal/store"
)

// 设置项 key。
const (
	keyRates       = "fx_rates_json"     // 数据源给出的汇率(JSON,EUR 基准)
	keyOverrides   = "fx_overrides_json" // 用户手动指定的汇率,优先于数据源
	keyAutoUpdate  = "fx_auto_update"
	keySource      = "fx_source"
	keyRateDate    = "fx_rate_date"
	keyUpdatedAt   = "fx_updated_at"
	keyLastAttempt = "fx_last_attempt_at"
	keyLastError   = "fx_last_error"
)

// 汇率来源。
const (
	SourceOpenER  = "open.er-api.com"
	SourceECB     = "ecb"
	SourceManual  = "manual" // 旧版本手动保存的汇率
	SourceDefault = "default"
)

// staleAfter 是自动更新的最小间隔:数据源每天只更新一次。
const staleAfter = 12 * time.Hour

// defaultRates 只在从未成功拉取过汇率时使用。
var defaultRates = money.Rates{"USD": 1.10, "CNY": 7.90, "RUB": 90.0}

// legacyRateKeys 是早期版本逐个币种保存汇率的设置项。
var legacyRateKeys = map[string]string{
	"USD": "fx_eur_to_usd",
	"CNY": "fx_eur_to_cny",
	"RUB": "fx_eur_to_rub",
}

// State 是某一时刻的汇率快照。
type State struct {
	Rates      money.Rates // 实际用于换算的汇率(数据源 + 手动覆盖)
	Market     money.Rates // 数据源给出的汇率
	Overrides  money.Rates // 手动覆盖
	AutoUpdate bool
	Source     string
	RateDate   string
	UpdatedAt  string
	LastError  string

	lastAttempt time.Time
}

// Service 提供汇率的读取与更新。
type Service struct {
	store  *store.Store
	client *http.Client

	refreshing sync.Mutex // 保证同一时间只有一次拉取
}

// New 创建汇率服务。
func New(st *store.Store) *Service {
	return &Service{store: st, client: &http.Client{Timeout: 15 * time.Second}}
}

// Init 确保汇率相关的设置项存在,并接管旧版本留下的数据。
func (s *Service) Init(ctx context.Context) error {
	values := map[string]string{}
	if _, ok := s.store.Get(keyAutoUpdate); !ok {
		values[keyAutoUpdate] = store.BoolValue(true)
	}
	if _, ok := s.store.Get(keyRates); !ok {
		rates := defaultRates.Clone()
		source := SourceDefault
		for code, key := range legacyRateKeys {
			if rate, ok := parsePositive(s.store.String(key, "")); ok {
				rates[code] = rate
				source = SourceManual
			}
		}
		values[keyRates] = encodeRates(rates)
		if _, ok := s.store.Get(keySource); !ok {
			values[keySource] = source
		}
	}
	return s.store.SetMany(ctx, values)
}

// State 返回当前的汇率快照。
func (s *Service) State() State {
	market := decodeRates(s.store.String(keyRates, ""))
	overrides := decodeRates(s.store.String(keyOverrides, ""))

	effective := market.Clone()
	for code, rate := range overrides {
		effective[code] = rate
	}
	effective["EUR"] = 1

	lastAttempt, _ := time.Parse(time.RFC3339, s.store.String(keyLastAttempt, ""))
	return State{
		Rates:       effective,
		Market:      market,
		Overrides:   overrides,
		AutoUpdate:  s.store.Bool(keyAutoUpdate, true),
		Source:      s.store.String(keySource, SourceDefault),
		RateDate:    s.store.String(keyRateDate, ""),
		UpdatedAt:   s.store.String(keyUpdatedAt, ""),
		LastError:   s.store.String(keyLastError, ""),
		lastAttempt: lastAttempt,
	}
}

// SetAutoUpdate 开关自动更新。
func (s *Service) SetAutoUpdate(ctx context.Context, on bool) error {
	return s.store.Set(ctx, keyAutoUpdate, store.BoolValue(on))
}

// SetOverrides 整体替换手动汇率;传入空表即清除全部覆盖。
func (s *Service) SetOverrides(ctx context.Context, overrides money.Rates) error {
	return s.store.Set(ctx, keyOverrides, encodeRates(overrides))
}

// RefreshIfStale 在开启自动更新且距上次尝试已超过 staleAfter 时拉取汇率,供后台任务调用。
func (s *Service) RefreshIfStale(ctx context.Context) {
	state := s.State()
	if !state.AutoUpdate || time.Since(state.lastAttempt) < staleAfter {
		return
	}
	if err := s.Refresh(ctx); err != nil {
		log.Printf("fx: auto refresh failed: %v", err)
	}
}

// Refresh 立即拉取汇率:首选覆盖币种最全的 open.er-api.com,失败时退回欧洲央行。
// 数据源没有给出的币种保留原值。
func (s *Service) Refresh(ctx context.Context) error {
	if !s.refreshing.TryLock() {
		return errors.New("汇率正在更新中")
	}
	defer s.refreshing.Unlock()

	now := time.Now().Format(time.RFC3339)
	result, err := s.fetch(ctx)
	if err != nil {
		// 记录失败原因,保留上一次成功的汇率继续使用。
		if saveErr := s.store.SetMany(ctx, map[string]string{
			keyLastAttempt: now,
			keyLastError:   truncate(err.Error(), 300),
		}); saveErr != nil {
			log.Printf("fx: save refresh error: %v", saveErr)
		}
		return err
	}

	rates := decodeRates(s.store.String(keyRates, ""))
	for code, rate := range result.rates {
		rates[code] = rate
	}
	return s.store.SetMany(ctx, map[string]string{
		keyRates:       encodeRates(rates),
		keySource:      result.source,
		keyRateDate:    result.date,
		keyUpdatedAt:   now,
		keyLastAttempt: now,
		keyLastError:   "",
	})
}

func (s *Service) fetch(ctx context.Context) (fetched, error) {
	primary, err := fetchOpenER(ctx, s.client)
	if err == nil {
		return primary, nil
	}
	fallback, fallbackErr := fetchECB(ctx, s.client)
	if fallbackErr == nil {
		return fallback, nil
	}
	return fetched{}, errors.Join(err, fallbackErr)
}

// encodeRates 序列化汇率表,只保留受支持币种的有效汇率(EUR 恒为 1,不存)。
func encodeRates(rates money.Rates) string {
	clean := make(money.Rates, len(rates))
	for code, rate := range rates {
		code = money.Normalize(code)
		if code != "EUR" && money.IsSupported(code) && money.Positive(rate) {
			clean[code] = rate
		}
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeRates 是 encodeRates 的逆操作;内容损坏时返回空表。
func decodeRates(raw string) money.Rates {
	var parsed map[string]float64
	_ = json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed)

	out := make(money.Rates, len(parsed))
	for code, rate := range parsed {
		code = money.Normalize(code)
		if code != "EUR" && money.IsSupported(code) && money.Positive(rate) {
			out[code] = rate
		}
	}
	return out
}

func parsePositive(raw string) (float64, bool) {
	var f float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &f); err != nil {
		return 0, false
	}
	return f, money.Positive(f)
}

func truncate(s string, max int) string {
	if runes := []rune(s); len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
