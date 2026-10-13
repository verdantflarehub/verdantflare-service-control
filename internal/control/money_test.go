package control

import (
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

func TestGatewayQuotaToCNYUsesFixedPoint(t *testing.T) {
	for _, item := range []struct {
		quota int64
		want  string
	}{
		{0, "0.00"}, {1, "0.000015"}, {362, "0.005285"},
		{500000, "7.30"}, {2500000, "36.50"}, {2499638, "36.494715"},
	} {
		if got := quotaYuan(item.quota); got != item.want {
			t.Errorf("quota %d => %s, want %s", item.quota, got, item.want)
		}
	}
	balance := creditBalance(gateway.CenterBalance{RemainingQuota: 2499638, UsedQuota: 362, Enabled: true})
	if balance.Currency != "CNY" || balance.BudgetYuan != "36.50" || balance.UsedYuan != "0.005285" || balance.RemainingYuan != "36.494715" {
		t.Fatalf("inconsistent Control amounts: %+v", balance)
	}
}

func TestCNYFenGatewayCentsBoundary(t *testing.T) {
	for _, item := range []struct {
		fen   int
		cents int
		ok    bool
	}{
		{0, 0, false}, {1, 0, false}, {4, 1, true},
		{1000, 137, true}, {3650, 500, true}, {730000, 100000, true}, {730001, 0, false},
	} {
		got, ok := gatewayCentsFromFen(item.fen)
		if got != item.cents || ok != item.ok {
			t.Errorf("fen %d => %d %t, want %d %t", item.fen, got, ok, item.cents, item.ok)
		}
	}
	if got := creditQuote(1000, 137); got.CreditedYuan != "10.001" {
		t.Fatalf("CNY quote lost cent-rounding: %+v", got)
	}
}
