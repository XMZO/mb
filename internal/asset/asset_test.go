package asset

import (
	"testing"
	"time"
)

func TestAddMonthsClampsToMonthEnd(t *testing.T) {
	cases := []struct {
		from   string
		months int
		want   string
	}{
		{"2026-01-15", 1, "2026-02-15"},
		{"2026-01-31", 1, "2026-02-28"}, // 2 月没有 31 日
		{"2024-01-31", 1, "2024-02-29"}, // 闰年
		{"2026-11-30", 3, "2027-02-28"},
		{"2026-03-31", 12, "2027-03-31"},
		{"2024-02-29", 12, "2025-02-28"},
	}
	for _, c := range cases {
		got, err := AddMonths(c.from, c.months)
		if err != nil || got != c.want {
			t.Errorf("AddMonths(%q, %d) = %q, %v; want %q", c.from, c.months, got, err, c.want)
		}
	}
	if _, err := AddMonths("not-a-date", 1); err == nil {
		t.Error("AddMonths should reject invalid dates")
	}
}

func TestDaysLeft(t *testing.T) {
	now := time.Date(2026, 11, 12, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))
	cases := []struct {
		expires string
		days    int
		ok      bool
	}{
		{"2026-11-12", 0, true},
		{"2026-11-13", 1, true},
		{"2026-11-01", -11, true},
		{"2027-11-12", 365, true},
		{Permanent, 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		days, ok := DaysLeft(c.expires, now)
		if days != c.days || ok != c.ok {
			t.Errorf("DaysLeft(%q) = %d, %v; want %d, %v", c.expires, days, ok, c.days, c.ok)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	for _, input := range []string{"永久", " Permanent ", "lifetime", Permanent} {
		if got, err := ParseExpiry(input); err != nil || got != Permanent {
			t.Errorf("ParseExpiry(%q) = %q, %v; want permanent", input, got, err)
		}
	}
	if got, err := ParseExpiry(" 2026-12-01 "); err != nil || got != "2026-12-01" {
		t.Errorf("ParseExpiry(date) = %q, %v", got, err)
	}
	for _, input := range []string{"", "2026-13-01", "2026/12/01", "tomorrow"} {
		if _, err := ParseExpiry(input); err == nil {
			t.Errorf("ParseExpiry(%q) should fail", input)
		}
	}
}

func TestDisksJSONRoundTrip(t *testing.T) {
	disks := []Disk{{SizeGB: 80, Type: "ssd"}, {SizeGB: 0, Type: "HDD"}, {SizeGB: 500, Type: "tape"}}
	raw := DisksJSON(disks)
	got := ParseDisksJSON(raw)
	if len(got) != 2 || got[0] != (Disk{80, "SSD"}) || got[1] != (Disk{500, ""}) {
		t.Fatalf("round trip = %+v (raw %s)", got, raw)
	}
	if DisksJSON(nil) != "" || ParseDisksJSON("{broken") != nil {
		t.Error("empty and broken input should produce empty results")
	}
	if got[0].String() != "80GB SSD" || got[1].String() != "500GB" {
		t.Errorf("unexpected labels: %q, %q", got[0], got[1])
	}
}

func TestAnnualizeAndBillingLabel(t *testing.T) {
	if got := Annualize(10, 1); got != 120 {
		t.Errorf("monthly 10 annualized = %v, want 120", got)
	}
	if got := Annualize(90, 36); got != 30 {
		t.Errorf("triennial 90 annualized = %v, want 30", got)
	}
	if Annualize(10, 0) != 0 {
		t.Error("invalid billing period should annualize to 0")
	}
	if BillingLabel(1) != "月付" || BillingLabel(12) != "年付" || BillingLabel(5) != "5 个月" {
		t.Error("unexpected billing labels")
	}
}
