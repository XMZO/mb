package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mb/internal/money"
	"mb/internal/store"
)

const openERBody = `{"result":"success","base_code":"EUR","time_last_update_utc":"Thu, 12 Nov 2026 00:02:31 +0000",
	"rates":{"EUR":1,"USD":1.2,"CNY":8.5,"TRY":40,"XXX":9,"JPY":-1}}`

const ecbBody = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <gesmes:subject>Reference rates</gesmes:subject>
  <Cube>
    <Cube time="2026-11-11">
      <Cube currency="USD" rate="1.1"/>
      <Cube currency="CNY" rate="8.0"/>
      <Cube currency="GBP" rate="0.85"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(st)
	if err := svc.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc, st
}

// serve 把两个数据源指向本地测试服务;status 为 0 时返回正常内容。
func serve(t *testing.T, openERStatus, ecbStatus int) {
	t.Helper()
	handler := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if status != 0 {
				http.Error(w, "unavailable", status)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/opener", handler(openERStatus, openERBody))
	mux.HandleFunc("/ecb", handler(ecbStatus, ecbBody))
	server := httptest.NewServer(mux)

	oldOpenER, oldECB := openERURL, ecbURL
	openERURL, ecbURL = server.URL+"/opener", server.URL+"/ecb"
	t.Cleanup(func() {
		openERURL, ecbURL = oldOpenER, oldECB
		server.Close()
	})
}

func TestInitSeedsDefaults(t *testing.T) {
	svc, _ := newService(t)
	state := svc.State()
	if !state.AutoUpdate || state.Source != SourceDefault {
		t.Errorf("unexpected initial state: %+v", state)
	}
	if got, ok := state.Rates.Convert(1.1, "USD", "EUR"); !ok || got < 0.99 || got > 1.01 {
		t.Errorf("default USD rate not usable: %v, %v", got, ok)
	}
}

func TestInitAdoptsLegacyRates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Set(context.Background(), "fx_eur_to_usd", "1.3333"); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	if err := svc.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rate, _ := svc.State().Rates.Rate("USD"); rate != 1.3333 {
		t.Errorf("legacy rate not adopted: %v", rate)
	}
}

func TestRefreshUsesPrimarySource(t *testing.T) {
	serve(t, 0, 0)
	svc, _ := newService(t)
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := svc.State()
	if state.Source != SourceOpenER || state.LastError != "" || state.UpdatedAt == "" || state.RateDate != "2026-11-12" {
		t.Errorf("unexpected state: %+v", state)
	}
	if rate, _ := state.Rates.Rate("TRY"); rate != 40 {
		t.Errorf("TRY = %v, want 40", rate)
	}
	// 不受支持的币种和非法数值被丢弃。
	if _, ok := state.Market["XXX"]; ok {
		t.Error("unsupported currency was stored")
	}
	if _, ok := state.Rates.Rate("JPY"); ok {
		t.Error("invalid rate was stored")
	}
}

func TestRefreshFallsBackToECBAndKeepsOtherRates(t *testing.T) {
	serve(t, 0, 0)
	svc, _ := newService(t)
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	serve(t, http.StatusBadGateway, 0) // 主数据源故障
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("fallback refresh: %v", err)
	}
	state := svc.State()
	if state.Source != SourceECB || state.RateDate != "2026-11-11" {
		t.Errorf("unexpected state: %+v", state)
	}
	if rate, _ := state.Rates.Rate("USD"); rate != 1.1 {
		t.Errorf("USD = %v, want the ECB rate 1.1", rate)
	}
	// 欧洲央行不提供 TRY:应保留上一次拉到的值,而不是丢掉。
	if rate, _ := state.Rates.Rate("TRY"); rate != 40 {
		t.Errorf("TRY = %v, want the previous rate 40", rate)
	}
}

func TestRefreshFailureKeepsRatesAndRecordsError(t *testing.T) {
	serve(t, http.StatusInternalServerError, http.StatusInternalServerError)
	svc, _ := newService(t)
	before := svc.State().Rates

	if err := svc.Refresh(context.Background()); err == nil {
		t.Fatal("refresh should fail when both sources are down")
	}
	state := svc.State()
	if state.LastError == "" {
		t.Error("failure reason was not recorded")
	}
	if rate, _ := state.Rates.Rate("USD"); rate != before["USD"] {
		t.Error("rates must be kept when refresh fails")
	}
	// 刚失败过,自动更新不应立刻重试。
	svc.RefreshIfStale(context.Background())
}

func TestOverridesWinAndSurviveRefresh(t *testing.T) {
	serve(t, 0, 0)
	svc, _ := newService(t)
	ctx := context.Background()

	if err := svc.SetOverrides(ctx, money.Rates{"USD": 2, "EUR": 5, "XXX": 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	state := svc.State()
	if rate, _ := state.Rates.Rate("USD"); rate != 2 {
		t.Errorf("override lost after refresh: USD = %v", rate)
	}
	if state.Market["USD"] != 1.2 {
		t.Errorf("market rate should still be tracked: %v", state.Market["USD"])
	}
	if len(state.Overrides) != 1 {
		t.Errorf("invalid overrides were stored: %v", state.Overrides)
	}

	if err := svc.SetOverrides(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if rate, _ := svc.State().Rates.Rate("USD"); rate != 1.2 {
		t.Errorf("after clearing overrides USD = %v, want 1.2", rate)
	}
}
