package mb

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func parseMoney(s string) (float64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, nil
	}
	switch strings.ToLower(raw) {
	case "免费", "free":
		return 0, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if !isFiniteNonNegative(f) {
		return 0, errors.New("invalid money")
	}
	return f, nil
}
func isSupportedCurrency(c string) bool {
	switch c {
	case "CNY", "USD", "EUR":
		return true
	case "RUB":
		return true
	default:
		return false
	}
}
func baseCurrencyFromRequest(r *http.Request) string {
	base := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("base")))
	if base == "" {
		return "CNY"
	}
	if !isSupportedCurrency(base) {
		return "CNY"
	}
	return base
}
func currencySymbol(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	switch currency {
	case "CNY":
		return "\uFFE5"
	case "USD":
		return "$"
	case "EUR":
		return "\u20AC"
	case "RUB":
		return "\u20BD"
	default:
		return ""
	}
}
func convertMoney(amount float64, fromCurrency, toCurrency string, fx fxInfo) float64 {
	if !isFiniteNonNegative(amount) {
		return 0
	}
	from := strings.ToUpper(strings.TrimSpace(fromCurrency))
	to := strings.ToUpper(strings.TrimSpace(toCurrency))
	if from == "" {
		from = "CNY"
	}
	if to == "" {
		to = "CNY"
	}
	if from == to {
		return amount
	}
	if !isSupportedCurrency(from) || !isSupportedCurrency(to) {
		return 0
	}
	if (from == "USD" || to == "USD") && !isFinitePositive(fx.EURToUSD) {
		return 0
	}
	if (from == "CNY" || to == "CNY") && !isFinitePositive(fx.EURToCNY) {
		return 0
	}
	if (from == "RUB" || to == "RUB") && !isFinitePositive(fx.EURToRUB) {
		return 0
	}

	var inEUR float64
	switch from {
	case "EUR":
		inEUR = amount
	case "USD":
		inEUR = amount / fx.EURToUSD
	case "CNY":
		inEUR = amount / fx.EURToCNY
	case "RUB":
		inEUR = amount / fx.EURToRUB
	default:
		return 0
	}
	switch to {
	case "EUR":
		return inEUR
	case "USD":
		return inEUR * fx.EURToUSD
	case "CNY":
		return inEUR * fx.EURToCNY
	case "RUB":
		return inEUR * fx.EURToRUB
	default:
		return 0
	}
}
