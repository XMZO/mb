// Package reminder 在域名 / VPS 临近到期时发送邮件提醒。
//
// 提醒规则:用户配置一组"提前天数"(如 30、7、1)。某项资产距到期的天数落入某一档
// (不超过该档天数的最小一档)时,为这一档发送一次提醒。判断依据是"这一档是否已经提醒过",
// 而不是"今天是否恰好是那一天",所以服务停机错过的提醒会在恢复后补发。
package reminder

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"mb/internal/asset"
	"mb/internal/mailer"
	"mb/internal/money"
	"mb/internal/store"
	"mb/internal/usererr"
)

// 设置项 key。
const (
	keyEnabled = "email_enabled"
	keyDays    = "email_days_before"
)

const (
	defaultDays = "30"
	maxDays     = 3650 // 提前天数的上限(十年)

	logRetention = 2 * 365 * 24 * time.Hour // 提醒日志保留两年
)

// Settings 是提醒功能的开关与提前天数。
type Settings struct {
	Enabled bool
	Days    []int // 升序、无重复
}

// Load 读取提醒设置。
func Load(st *store.Store) Settings {
	days, err := ParseDays(st.String(keyDays, defaultDays))
	if err != nil {
		days, _ = ParseDays(defaultDays)
	}
	return Settings{Enabled: st.Bool(keyEnabled, false), Days: days}
}

// Save 保存提醒设置。
func Save(ctx context.Context, st *store.Store, s Settings) error {
	return st.SetMany(ctx, map[string]string{
		keyEnabled: store.BoolValue(s.Enabled),
		keyDays:    FormatDays(s.Days),
	})
}

// ParseDays 解析用逗号、空格等分隔的提前天数列表,返回升序去重后的结果;留空时使用默认值。
func ParseDays(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultDays
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune(",;，；、 \t\r\n", r)
	})
	seen := make(map[int]bool, len(parts))
	var days []int
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > maxDays {
			return nil, usererr.Newf("提醒天数应为 0-%d 之间的整数，多个值用逗号分隔", maxDays)
		}
		if !seen[n] {
			seen[n] = true
			days = append(days, n)
		}
	}
	if len(days) == 0 {
		return nil, usererr.New("请至少填写一个提醒天数")
	}
	sort.Ints(days)
	return days, nil
}

// FormatDays 把提前天数列表格式化为 "1,7,30"。
func FormatDays(days []int) string {
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

// Service 执行到期检查并发送提醒邮件。
type Service struct {
	store *store.Store

	// 以下两项仅为方便测试而可替换。
	send func(context.Context, mailer.Config, mailer.Message) error
	now  func() time.Time
}

// New 创建提醒服务。
func New(st *store.Store) *Service {
	return &Service{store: st, send: mailer.Send, now: time.Now}
}

// Run 检查一遍所有资产,把需要提醒的合并成一封邮件发出。提醒未启用时什么都不做。
func (s *Service) Run(ctx context.Context) error {
	settings := Load(s.store)
	if !settings.Enabled {
		return nil
	}
	cfg, err := mailer.LoadSettings(s.store).Config()
	if err != nil {
		return fmt.Errorf("邮件配置不完整: %w", err)
	}

	now := s.now()
	items, err := s.loadItems(ctx)
	if err != nil {
		return err
	}
	// 只有最大提前天数以内的日志才可能影响判断。
	longest := settings.Days[len(settings.Days)-1]
	sent, err := s.store.ReminderLog(ctx, now.AddDate(0, 0, -longest-1).Format(asset.DateLayout))
	if err != nil {
		return err
	}

	due := dueItems(items, settings.Days, sent, now)
	if len(due) == 0 {
		return nil
	}
	if err := s.send(ctx, cfg, compose(due, now)); err != nil {
		return err
	}

	// 邮件已经发出,必须记账,否则下一轮会重复发送。
	// 这里换用独立的 context:发信可能已经耗掉了 ctx 的大部分时限。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	records := make([]store.ReminderRecord, len(due))
	for i, d := range due {
		records[i] = store.ReminderRecord{Kind: d.kind, ID: d.id, DaysBefore: d.threshold, SentDate: asset.Today(now)}
	}
	if err := s.store.AddReminderLog(saveCtx, records); err != nil {
		return fmt.Errorf("提醒已发送但记录失败: %w", err)
	}
	return s.store.PruneReminderLog(saveCtx, now.Add(-logRetention).Format(asset.DateLayout))
}

// item 是一项可能需要提醒的资产。
type item struct {
	kind     asset.Kind
	id       int64
	title    string
	expires  string
	price    float64
	currency string
}

// dueItem 是本轮需要提醒的资产。
type dueItem struct {
	item
	daysLeft  int
	threshold int // 命中的提前天数档位
}

func (s *Service) loadItems(ctx context.Context) ([]item, error) {
	domains, err := s.store.Domains(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := s.store.VPSList(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, len(domains)+len(servers))
	for _, d := range domains {
		items = append(items, item{
			kind: asset.KindDomain, id: d.ID, title: withDetail(d.Name, d.Registrar),
			expires: d.ExpiresAt, price: d.Price, currency: d.Currency,
		})
	}
	for _, v := range servers {
		items = append(items, item{
			kind: asset.KindVPS, id: v.ID, title: withDetail(v.Vendor, v.IP),
			expires: v.ExpiresAt, price: v.Price, currency: v.Currency,
		})
	}
	return items, nil
}

func withDetail(name, detail string) string {
	if detail == "" {
		return name
	}
	return name + " (" + detail + ")"
}

// dueItems 选出本轮需要提醒的资产,按剩余天数从少到多排列。thresholds 必须升序。
func dueItems(items []item, thresholds []int, sent []store.ReminderRecord, now time.Time) []dueItem {
	type stageKey struct {
		kind      asset.Kind
		id        int64
		threshold int
	}
	lastSent := make(map[stageKey]string, len(sent))
	for _, rec := range sent {
		key := stageKey{rec.Kind, rec.ID, rec.DaysBefore}
		if rec.SentDate > lastSent[key] {
			lastSent[key] = rec.SentDate
		}
	}

	var due []dueItem
	for _, it := range items {
		days, ok := asset.DaysLeft(it.expires, now)
		if !ok || days < 0 {
			continue // 永久、日期无效或已过期的不再提醒
		}
		threshold, ok := stageFor(thresholds, days)
		if !ok {
			continue
		}
		// 这一档的提醒窗口从"到期日前 threshold 天"开始;窗口内提醒过就不再重复。
		// 续费后到期日后移,窗口随之后移,去年的记录自然失效。
		expires, _ := time.Parse(asset.DateLayout, it.expires)
		windowStart := expires.AddDate(0, 0, -threshold).Format(asset.DateLayout)
		if lastSent[stageKey{it.kind, it.id, threshold}] >= windowStart {
			continue
		}
		due = append(due, dueItem{item: it, daysLeft: days, threshold: threshold})
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].daysLeft < due[j].daysLeft })
	return due
}

// stageFor 返回不小于 days 的最小档位。
func stageFor(thresholds []int, days int) (int, bool) {
	for _, t := range thresholds {
		if t >= days {
			return t, true
		}
	}
	return 0, false
}

// compose 把待提醒的资产写成一封邮件。
func compose(due []dueItem, now time.Time) mailer.Message {
	subject := fmt.Sprintf("到期提醒：%d 项资产即将到期", len(due))
	if due[0].daysLeft == 0 {
		subject += "（含今日到期）"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "以下资产即将到期（%s）：\n", asset.Today(now))
	for _, kind := range []asset.Kind{asset.KindDomain, asset.KindVPS} {
		wroteTitle := false
		for _, d := range due {
			if d.kind != kind {
				continue
			}
			if !wroteTitle {
				fmt.Fprintf(&b, "\n【%s】\n", kind.Label())
				wroteTitle = true
			}
			fmt.Fprintf(&b, "- %s ｜ %s ｜ %s ｜ %s\n", d.title, d.expires, daysLabel(d.daysLeft), priceLabel(d.price, d.currency))
		}
	}
	b.WriteString("\n—— MB 资产面板\n")
	return mailer.Message{Subject: subject, Body: b.String()}
}

func daysLabel(days int) string {
	if days == 0 {
		return "今天到期"
	}
	return fmt.Sprintf("剩余 %d 天", days)
}

func priceLabel(price float64, currency string) string {
	if price == 0 {
		return "免费"
	}
	return money.Format(price) + " " + currency
}
