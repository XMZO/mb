package reminder

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mb/internal/asset"
	"mb/internal/mailer"
	"mb/internal/store"
)

func day(s string) time.Time {
	t, err := time.ParseInLocation(asset.DateLayout, s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t.Add(9 * time.Hour)
}

func TestParseDays(t *testing.T) {
	days, err := ParseDays(" 30, 7；1 7\n0 ")
	if err != nil || FormatDays(days) != "0,1,7,30" {
		t.Errorf("ParseDays = %v, %v", days, err)
	}
	if days, _ := ParseDays(""); FormatDays(days) != "30" {
		t.Errorf("empty input should fall back to the default, got %v", days)
	}
	for _, bad := range []string{"abc", "-1", "7,x", "99999"} {
		if _, err := ParseDays(bad); err == nil {
			t.Errorf("ParseDays(%q) should fail", bad)
		}
	}
}

func TestDueItems(t *testing.T) {
	now := day("2026-11-12")
	thresholds := []int{1, 7, 30}
	items := []item{
		{kind: asset.KindDomain, id: 1, title: "in-20-days", expires: "2026-12-02"},
		{kind: asset.KindDomain, id: 2, title: "in-5-days", expires: "2026-11-17"},
		{kind: asset.KindDomain, id: 3, title: "today", expires: "2026-11-12"},
		{kind: asset.KindDomain, id: 4, title: "far-away", expires: "2027-06-01"},
		{kind: asset.KindDomain, id: 5, title: "expired", expires: "2026-11-01"},
		{kind: asset.KindDomain, id: 6, title: "forever", expires: asset.Permanent},
		{kind: asset.KindVPS, id: 1, title: "vps-in-5-days", expires: "2026-11-17"},
	}

	due := dueItems(items, thresholds, nil, now)
	got := map[string]int{}
	for _, d := range due {
		got[d.title] = d.threshold
	}
	want := map[string]int{"in-20-days": 30, "in-5-days": 7, "today": 1, "vps-in-5-days": 7}
	if len(got) != len(want) {
		t.Fatalf("due = %v, want %v", got, want)
	}
	for title, threshold := range want {
		if got[title] != threshold {
			t.Errorf("%s: threshold = %d, want %d", title, got[title], threshold)
		}
	}
	if due[0].title != "today" {
		t.Errorf("most urgent item should come first, got %s", due[0].title)
	}

	// 这一档已经提醒过的不再重复;不同类型、不同档位互不影响。
	sent := []store.ReminderRecord{
		{Kind: asset.KindDomain, ID: 2, DaysBefore: 7, SentDate: "2026-11-10"},
		{Kind: asset.KindDomain, ID: 1, DaysBefore: 7, SentDate: "2026-11-10"},
	}
	again := dueItems(items, thresholds, sent, now)
	for _, d := range again {
		if d.title == "in-5-days" {
			t.Error("item already reminded at this stage was selected again")
		}
	}
	if len(again) != 3 {
		t.Errorf("due after log = %d items, want 3", len(again))
	}

	// 去年同一档的记录不妨碍今年再次提醒(续费后到期日后移)。
	lastYear := []store.ReminderRecord{{Kind: asset.KindDomain, ID: 2, DaysBefore: 7, SentDate: "2025-11-12"}}
	if len(dueItems(items[1:2], thresholds, lastYear, now)) != 1 {
		t.Error("a reminder from the previous renewal cycle must not suppress this one")
	}
}

func newService(t *testing.T, now time.Time) (*Service, *store.Store, *[]mailer.Message) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()

	if err := mailer.SaveSettings(ctx, st, mailer.Settings{Host: "smtp.example.com", Username: "me@example.com", To: "me@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, st, Settings{Enabled: true, Days: []int{7, 30}}); err != nil {
		t.Fatal(err)
	}

	var sent []mailer.Message
	svc := New(st)
	svc.now = func() time.Time { return now }
	svc.send = func(_ context.Context, _ mailer.Config, msg mailer.Message) error {
		sent = append(sent, msg)
		return nil
	}
	return svc, st, &sent
}

func TestRunSendsOnceAndCatchesUp(t *testing.T) {
	now := day("2026-11-12")
	svc, st, sent := newService(t, now)
	ctx := context.Background()

	id, err := st.CreateDomain(ctx, asset.Domain{Name: "soon.com", Registrar: "R", ExpiresAt: "2026-12-02", Price: 9.5, Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVPS(ctx, asset.VPS{Vendor: "Later", ExpiresAt: "2027-06-01", Currency: "CNY", BillingMonths: 12}); err != nil {
		t.Fatal(err)
	}

	// 距到期 20 天:已经进入"提前 30 天"这一档,即使错过了恰好 30 天的那一天也要补发。
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(*sent))
	}
	msg := (*sent)[0]
	if !strings.Contains(msg.Body, "soon.com (R)") || !strings.Contains(msg.Body, "剩余 20 天") || !strings.Contains(msg.Body, "9.5 USD") {
		t.Errorf("unexpected body:\n%s", msg.Body)
	}
	if strings.Contains(msg.Body, "Later") {
		t.Error("asset outside every reminder window was included")
	}

	// 同一档不重复发送。
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("reminder was sent again: %d messages", len(*sent))
	}

	// 进入下一档(提前 7 天)时再发一次。
	svc.now = func() time.Time { return day("2026-11-27") }
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 2 {
		t.Fatalf("next stage: sent %d messages, want 2", len(*sent))
	}

	// 续费之后,同一档在新的周期里重新生效。
	if err := st.ExtendExpiry(ctx, asset.KindDomain, []int64{id}, 12); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return day("2027-11-10") }
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 3 {
		t.Fatalf("after renewal: sent %d messages, want 3", len(*sent))
	}
}

func TestRunDoesNotLogWhenSendFails(t *testing.T) {
	now := day("2026-11-12")
	svc, st, sent := newService(t, now)
	ctx := context.Background()
	if _, err := st.CreateDomain(ctx, asset.Domain{Name: "soon.com", ExpiresAt: "2026-11-15", Currency: "CNY"}); err != nil {
		t.Fatal(err)
	}

	deliver := svc.send
	svc.send = func(context.Context, mailer.Config, mailer.Message) error { return errors.New("smtp down") }
	if err := svc.Run(ctx); err == nil {
		t.Fatal("Run should report the send failure")
	}

	// 发送失败不记账,下一轮会重试。
	svc.send = deliver
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("retry sent %d messages, want 1", len(*sent))
	}
}

func TestRunDisabledOrIncomplete(t *testing.T) {
	svc, st, sent := newService(t, day("2026-11-12"))
	ctx := context.Background()
	if _, err := st.CreateDomain(ctx, asset.Domain{Name: "soon.com", ExpiresAt: "2026-11-15", Currency: "CNY"}); err != nil {
		t.Fatal(err)
	}

	if err := Save(ctx, st, Settings{Enabled: false, Days: []int{7}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Run(ctx); err != nil || len(*sent) != 0 {
		t.Fatalf("disabled reminders: err=%v sent=%d", err, len(*sent))
	}

	if err := Save(ctx, st, Settings{Enabled: true, Days: []int{7}}); err != nil {
		t.Fatal(err)
	}
	if err := mailer.SaveSettings(ctx, st, mailer.Settings{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Run(ctx); err == nil {
		t.Error("incomplete mail settings should be reported")
	}
}
