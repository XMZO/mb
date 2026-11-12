package asset

import (
	"strings"
	"time"

	"mb/internal/usererr"
)

const (
	// Permanent 是"永久有效"在存储中的表示。
	Permanent = "9999-12-31"

	// DateLayout 是到期日的存储与展示格式。
	DateLayout = "2006-01-02"
)

// permanentTokens 是用户输入、历史数据中表示"永久"的写法。
var permanentTokens = map[string]bool{
	"永久": true, "无限期": true, "长期": true,
	"permanent": true, "forever": true, "lifetime": true,
}

// IsPermanent 判断到期日是否为永久。
func IsPermanent(expires string) bool {
	return strings.TrimSpace(expires) == Permanent
}

// CanonicalExpiry 用于读取历史数据:把"永久"等写法统一成 Permanent,其余去空白后原样返回。
func CanonicalExpiry(raw string) string {
	s := strings.TrimSpace(raw)
	if permanentTokens[strings.ToLower(s)] {
		return Permanent
	}
	return s
}

// ParseExpiry 校验用户输入的到期日,返回 YYYY-MM-DD 或 Permanent。
func ParseExpiry(raw string) (string, error) {
	s := CanonicalExpiry(raw)
	if s == "" {
		return "", usererr.New("到期时间不能为空")
	}
	if s == Permanent {
		return Permanent, nil
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return "", usererr.New("到期时间格式应为 YYYY-MM-DD，或选择永久")
	}
	return t.Format(DateLayout), nil
}

// DaysLeft 返回距离到期的天数:今天到期为 0,已过期为负数。
// 永久或日期无法解析时 ok 为 false。
func DaysLeft(expires string, now time.Time) (days int, ok bool) {
	if IsPermanent(expires) {
		return 0, false
	}
	t, err := time.Parse(DateLayout, strings.TrimSpace(expires))
	if err != nil {
		return 0, false
	}
	// 两边都用 UTC 零点表示"日历上的一天",避免夏令时造成的 23/25 小时误差。
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int(t.Sub(today) / (24 * time.Hour)), true
}

// AddMonths 把到期日顺延 months 个月;目标月份没有对应日期时取月末
// (1 月 31 日 + 1 个月 = 2 月 28/29 日)。
func AddMonths(expires string, months int) (string, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(expires))
	if err != nil {
		return "", err
	}
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	if last := first.AddDate(0, 1, -1).Day(); d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC).Format(DateLayout), nil
}

// Today 返回 now 所在时区的当天日期。
func Today(now time.Time) string { return now.Format(DateLayout) }
