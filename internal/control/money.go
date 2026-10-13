package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

const (
	cnyPerUSD    = "7.3"
	maxCreditFen = 730000
)

// The gateway accounts in USD cents and quota; the Control-facing money contract is CNY.
// 500000 quota = USD 1 = CNY 7.3. Keep arithmetic integral to avoid float drift.
func quotaYuan(quota int64) string {
	if quota < 0 {
		quota = 0
	}
	micros := (quota*73 + 2) / 5
	return microYuan(micros)
}

func microYuan(micros int64) string {
	fraction := fmt.Sprintf("%06d", micros%1_000_000)
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) < 2 {
		fraction += strings.Repeat("0", 2-len(fraction))
	}
	return strconv.FormatInt(micros/1_000_000, 10) + "." + fraction
}

func gatewayCentsFromFen(fen int) (int, bool) {
	if fen < 1 || fen > maxCreditFen {
		return 0, false
	}
	cents := (fen*10 + 36) / 73
	return cents, cents >= 1 && cents <= 100000
}

type APICreditBalance struct {
	Currency              string `json:"currency"`
	ExchangeRateCnyPerUSD string `json:"exchangeRateCnyPerUsd"`
	BudgetYuan            string `json:"budgetYuan"`
	UsedYuan              string `json:"usedYuan"`
	RemainingYuan         string `json:"remainingYuan"`
	RemainingQuota        int    `json:"remainingQuota"`
	UsedQuota             int    `json:"usedQuota"`
	Enabled               bool   `json:"enabled"`
}

func creditBalance(balance gateway.CenterBalance) APICreditBalance {
	return APICreditBalance{
		Currency: "CNY", ExchangeRateCnyPerUSD: cnyPerUSD,
		BudgetYuan: quotaYuan(int64(balance.RemainingQuota) + int64(balance.UsedQuota)),
		UsedYuan:   quotaYuan(int64(balance.UsedQuota)), RemainingYuan: quotaYuan(int64(balance.RemainingQuota)),
		RemainingQuota: balance.RemainingQuota, UsedQuota: balance.UsedQuota, Enabled: balance.Enabled,
	}
}

type APICreditQuote struct {
	Currency              string `json:"currency"`
	ExchangeRateCnyPerUSD string `json:"exchangeRateCnyPerUsd"`
	RequestedFen          int    `json:"requestedFen"`
	CreditedYuan          string `json:"creditedYuan"`
}

func creditQuote(fen int, gatewayCents int) APICreditQuote {
	return APICreditQuote{Currency: "CNY", ExchangeRateCnyPerUSD: cnyPerUSD,
		RequestedFen: fen, CreditedYuan: quotaYuan(int64(gatewayCents) * 5000)}
}

type APIKeyProbe struct {
	OK                    bool     `json:"ok"`
	Reason                string   `json:"reason"`
	Models                []string `json:"models"`
	Currency              string   `json:"currency"`
	ExchangeRateCnyPerUSD string   `json:"exchangeRateCnyPerUsd"`
	RemainingYuan         string   `json:"remainingYuan"`
	RemainingQuota        int      `json:"remainingQuota"`
	ReadOnly              bool     `json:"readOnly"`
}
