package mb

import (
	"errors"
	"strings"
	"time"
)

func parseISODate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
}
func isPermanentISODate(s string) bool {
	return strings.TrimSpace(s) == permanentISODate
}
func normalizeISODateOrPermanent(s string) string {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return ""
	}
	if isPermanentISODate(raw) {
		return permanentISODate
	}
	if isPermanentToken(raw) {
		return permanentISODate
	}
	return raw
}
func parseRenewDate(raw string) (string, error) {
	expiresAt := normalizeISODateOrPermanent(raw)
	if expiresAt == "" {
		return "", errors.New("到期时间不能为空")
	}
	if isPermanentISODate(expiresAt) {
		return permanentISODate, nil
	}
	if _, err := parseISODate(expiresAt); err != nil {
		return "", errors.New("到期时间格式应为 YYYY-MM-DD 或 选择永久")
	}
	return expiresAt, nil
}
func isPermanentToken(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "永久", "permanent", "forever", "lifetime", "无限期", "长期":
		return true
	default:
		return false
	}
}
func daysUntilISODate(iso string) int {
	t, err := parseISODate(iso)
	if err != nil {
		return 0
	}
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return int(t.Sub(today) / (24 * time.Hour))
}
func daysBadgeClass(days int) string {
	if days < 30 {
		return "text-bg-danger"
	}
	if days < 90 {
		return "text-bg-warning text-dark"
	}
	return "text-bg-secondary"
}
