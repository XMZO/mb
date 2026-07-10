package mb

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"
)

//go:embed web/templates/*.html
var templateFiles embed.FS

// mustTemplates 从 web/templates 加载页面模板。模板已经脱离 Go 字符串常量,
// 后续替换前端时可以只替换模板目录或替换这一层渲染器。
func mustTemplates() map[string]*template.Template {
	funcs := template.FuncMap{
		"money": func(f float64) string {
			if !isFiniteNonNegative(f) {
				return "0"
			}
			s := fmt.Sprintf("%.2f", f)
			s = strings.TrimRight(s, "0")
			s = strings.TrimRight(s, ".")
			if s == "" {
				return "0"
			}
			return s
		},
		"usdToCny": func(fx fxInfo) string {
			usd := fxRate(fx, "USD")
			cny := fxRate(fx, "CNY")
			if !isFinitePositive(usd) || !isFinitePositive(cny) {
				return "-"
			}
			return fmt.Sprintf("%.4f", cny/usd)
		},
		"fxRate": func(fx fxInfo, currency string) string {
			return fxRateLabel(fx, currency)
		},
		"currencyOptions": func() []currencyOption {
			return currencyOptions()
		},
		"manualFxCurrencies": func() []currencyOption {
			return manualFxCurrencies()
		},
		"featuredManualFxCurrencies": func() []currencyOption {
			return featuredManualFxCurrencies()
		},
		"additionalManualFxCurrencies": func() []currencyOption {
			return additionalManualFxCurrencies()
		},
		"daysLabel": func(days int) string {
			if days < 0 {
				return "已过期"
			}
			return fmt.Sprintf("剩余 %d 天", days)
		},
		"nowISO": func() string {
			return time.Now().In(time.Local).Format("2006-01-02")
		},
		"domainURL": func(domain string) string {
			return normalizeDomainURL(domain)
		},
	}

	base := template.New("base").Funcs(funcs)
	template.Must(base.ParseFS(templateFiles, "web/templates/layout.html"))

	makePage := func(pageFile string) *template.Template {
		t, err := base.Clone()
		if err != nil {
			panic(err)
		}
		template.Must(t.ParseFS(templateFiles, "web/templates/"+pageFile))
		return t
	}

	return map[string]*template.Template{
		"dashboard":  makePage("dashboard.html"),
		"login":      makePage("login.html"),
		"settings":   makePage("settings.html"),
		"asset_form": makePage("asset_form.html"),
	}
}
