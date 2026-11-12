package money

import (
	"math"
	"testing"
)

func TestConvert(t *testing.T) {
	rates := Rates{"USD": 1.25, "CNY": 8}

	if got, ok := rates.Convert(10, "USD", "CNY"); !ok || math.Abs(got-64) > 1e-9 {
		t.Errorf("10 USD -> CNY = %v, %v; want 64", got, ok)
	}
	if got, ok := rates.Convert(8, "CNY", "EUR"); !ok || math.Abs(got-1) > 1e-9 {
		t.Errorf("8 CNY -> EUR = %v, %v; want 1", got, ok)
	}
	if got, ok := rates.Convert(5, "TRY", "TRY"); !ok || got != 5 {
		t.Errorf("same-currency conversion must not need a rate, got %v, %v", got, ok)
	}
	// 缺少汇率时必须明确报告,而不是悄悄算成 0。
	if _, ok := rates.Convert(5, "TRY", "CNY"); ok {
		t.Error("conversion without a rate should report ok=false")
	}
}

func TestFormat(t *testing.T) {
	cases := map[float64]string{0: "0", 3: "3", 12.5: "12.5", 12.999: "13", 0.004: "0", 1234.56: "1234.56"}
	for in, want := range cases {
		if got := Format(in); got != want {
			t.Errorf("Format(%v) = %q, want %q", in, got, want)
		}
	}
	if got := FormatGrouped(1234567.5); got != "1,234,567.5" {
		t.Errorf("FormatGrouped = %q", got)
	}
	if got := FormatGrouped(999); got != "999" {
		t.Errorf("FormatGrouped = %q", got)
	}
	if got := WithSymbol("USD", 1200); got != "$1,200" {
		t.Errorf("WithSymbol(USD) = %q", got)
	}
	if got := WithSymbol("CHF", 12); got != "CHF 12" {
		t.Errorf("WithSymbol(CHF) = %q", got)
	}
	if got := FormatRate(0.000043); got != "0.000043" {
		t.Errorf("FormatRate(small) = %q", got)
	}
}

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]float64{"": 0, "免费": 0, "Free": 0, " 12.99 ": 12.99, "0": 0} {
		if got, err := ParseAmount(in); err != nil || got != want {
			t.Errorf("ParseAmount(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"-1", "abc", "NaN", "Inf"} {
		if _, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) should fail", in)
		}
	}
}

func TestCurrencyTable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All() {
		if c.Code == "" || c.Name == "" || seen[c.Code] {
			t.Errorf("bad or duplicate currency entry: %+v", c)
		}
		seen[c.Code] = true
	}
	if !IsSupported("CNY") || !IsSupported("EUR") || IsSupported("XXX") || IsSupported("cny") {
		t.Error("IsSupported expects normalized codes of known currencies")
	}
	if Normalize(" usd ") != "USD" {
		t.Error("Normalize should trim and upper-case")
	}
}
