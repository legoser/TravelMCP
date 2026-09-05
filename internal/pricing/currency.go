package pricing

import (
	"fmt"
	"strings"
)

type Currency string

const (
	CurrencyRUB Currency = "RUB"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyKZT Currency = "KZT"
	CurrencyBYN Currency = "BYN"
	CurrencyCNY Currency = "CNY"
)

var supported = map[Currency]bool{
	CurrencyRUB: true,
	CurrencyUSD: true,
	CurrencyEUR: true,
	CurrencyKZT: true,
	CurrencyBYN: true,
	CurrencyCNY: true,
}

var DefaultCurrencyVar Currency = CurrencyRUB

func NormalizeCurrency(s string) Currency {
	return Currency(strings.ToUpper(strings.TrimSpace(s)))
}

func ValidateCurrency(s string) error {
	c := NormalizeCurrency(s)
	if c == "" {
		return fmt.Errorf("currency empty")
	}
	if len(c) != 3 {
		return fmt.Errorf("currency %q: ISO 4217 — 3 буквы", s)
	}
	if !supported[c] {
		return fmt.Errorf("currency %q не поддерживается (поддерживаются RUB,USD,EUR,KZT,BYN,CNY)", s)
	}
	return nil
}

func IsSupported(c Currency) bool {
	return supported[NormalizeCurrency(string(c))]
}

func DefaultCurrency() Currency {
	if DefaultCurrencyVar != "" {
		return NormalizeCurrency(string(DefaultCurrencyVar))
	}
	return CurrencyRUB
}

func CurrencyOrDefault(c string) string {
	if c == "" {
		return string(DefaultCurrency())
	}
	n := NormalizeCurrency(c)
	if !IsSupported(n) {
		return string(DefaultCurrency())
	}
	return string(n)
}
