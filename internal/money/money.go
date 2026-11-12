// Package money 提供币种表、汇率换算与金额的解析/格式化。
package money

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// DefaultCurrency 是统计币种与新建资产的默认币种。
const DefaultCurrency = "CNY"

// Rates 是以欧元为基准的汇率表:1 EUR = Rates[code] 单位的 code。
type Rates map[string]float64

// Rate 返回 1 EUR 对应的 code 数量。
func (r Rates) Rate(code string) (float64, bool) {
	if code == "EUR" {
		return 1, true
	}
	v, ok := r[code]
	if !ok || !Positive(v) {
		return 0, false
	}
	return v, true
}

// Convert 把 amount 从 from 换算为 to;缺少任一币种的汇率时 ok 为 false。
func (r Rates) Convert(amount float64, from, to string) (float64, bool) {
	if from == to {
		return amount, true
	}
	fromRate, ok := r.Rate(from)
	if !ok {
		return 0, false
	}
	toRate, ok := r.Rate(to)
	if !ok {
		return 0, false
	}
	return amount / fromRate * toRate, true
}

// Clone 返回汇率表的副本。
func (r Rates) Clone() Rates {
	out := make(Rates, len(r))
	for code, rate := range r {
		out[code] = rate
	}
	return out
}

// Positive 判断 f 是否为有限的正数。
func Positive(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f > 0
}

// NonNegative 判断 f 是否为有限的非负数。
func NonNegative(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

// ParseAmount 解析用户输入的金额;空串、"免费"、"free" 视为 0。
func ParseAmount(s string) (float64, error) {
	raw := strings.TrimSpace(s)
	switch strings.ToLower(raw) {
	case "", "免费", "free":
		return 0, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if !NonNegative(f) {
		return 0, errors.New("money: amount must be a finite non-negative number")
	}
	return f, nil
}

// Format 保留至多两位小数并去掉多余的 0:12.50 -> "12.5",3.00 -> "3"。
func Format(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// FormatExact 原样输出数值,用于回填表单。
func FormatExact(f float64) string {
	if !NonNegative(f) {
		return "0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// FormatGrouped 在 Format 的基础上加千分位:1234567.5 -> "1,234,567.5"。
func FormatGrouped(f float64) string {
	s := Format(f)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	if hasFrac {
		b.WriteString("." + frac)
	}
	return sign + b.String()
}

// WithSymbol 为金额加上币种符号:"¥1,200"、"CHF 12"。
func WithSymbol(code string, amount float64) string {
	symbol := Symbol(code)
	if symbol == "" {
		return FormatGrouped(amount) + " " + code
	}
	// 以字母结尾的符号(CHF、RM、kr ...)与数字之间留一个空格。
	if last := []rune(symbol); unicode.IsLetter(last[len(last)-1]) {
		return symbol + " " + FormatGrouped(amount)
	}
	return symbol + FormatGrouped(amount)
}

// FormatRate 以合适的精度输出汇率:数值越小保留的小数位越多。
func FormatRate(rate float64) string {
	if !Positive(rate) {
		return "-"
	}
	digits := 4
	switch {
	case rate < 0.0001:
		digits = 8
	case rate < 0.01:
		digits = 6
	}
	s := strconv.FormatFloat(rate, 'f', digits, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
