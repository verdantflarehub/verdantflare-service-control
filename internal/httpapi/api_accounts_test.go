package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type accountGatewayFixture struct {
	balances  map[string]gateway.CenterBalance
	keys      map[string][]gateway.CenterKey
	credits   map[string][]gateway.CenterCreditGrant
	creditErr error
	grants    map[string]bool
	probes    int
	chatCalls atomic.Int32
	chatModel atomic.Value
	chatKey   atomic.Int32
	chatErr   error
}

func newAccountGatewayFixture() *accountGatewayFixture {
	return &accountGatewayFixture{balances: map[string]gateway.CenterBalance{}, keys: map[string][]gateway.CenterKey{}, credits: map[string][]gateway.CenterCreditGrant{}, grants: map[string]bool{}}
}
func (f *accountGatewayFixture) Balance(_ context.Context, org string) (gateway.CenterBalance, error) {
	return f.balances[org], nil
}
func (f *accountGatewayFixture) Grant(_ context.Context, org, id, _ string, amount int) (gateway.CenterBalance, error) {
	if !f.grants[id] {
		balance := f.balances[org]
		balance.RemainingQuota += amount * 5000
		balance.Enabled = true
		f.balances[org] = balance
		f.grants[id] = true
		f.credits[org] = append(f.credits[org], gateway.CenterCreditGrant{ID: id, AmountCents: amount, CreatedAt: time.Date(2026, 10, 4, 11, 34, 49, 0, time.UTC)})
	}
	return f.balances[org], nil
}
func (f *accountGatewayFixture) ListCreditGrants(_ context.Context, org string) ([]gateway.CenterCreditGrant, error) {
	return f.credits[org], f.creditErr
}
func (f *accountGatewayFixture) SetEnabled(_ context.Context, org string, enabled bool) error {
	balance := f.balances[org]
	balance.Enabled = enabled
	f.balances[org] = balance
	return nil
}
func (f *accountGatewayFixture) ListKeys(_ context.Context, org string) ([]gateway.CenterKey, error) {
	return f.keys[org], nil
}
func (f *accountGatewayFixture) CreateKey(_ context.Context, org, _, name string, models []string, _ int) (gateway.CenterCreatedKey, error) {
	key := gateway.CenterKey{ID: len(f.keys[org]) + 1, Name: name, Prefix: "abcd**********wxyz", Models: models, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(), Status: "active"}
	f.keys[org] = append(f.keys[org], key)
	return gateway.CenterCreatedKey{ID: key.ID, Name: name, Secret: "sk-test-secret", Models: models, CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt}, nil
}
func (f *accountGatewayFixture) RevokeKey(_ context.Context, org string, id int) error {
	for index := range f.keys[org] {
		if f.keys[org][index].ID == id {
			f.keys[org][index].Status = "revoked"
		}
	}
	return nil
}
func (f *accountGatewayFixture) ProbeKey(_ context.Context, org string, id int) (gateway.CenterProbe, error) {
	f.probes++
	for _, key := range f.keys[org] {
		if key.ID == id {
			reason := "ok"
			if key.Status != "active" {
				reason = key.Status
			} else if f.balances[org].RemainingQuota <= 0 {
				reason = "insufficient_quota"
			}
			return gateway.CenterProbe{OK: reason == "ok", Reason: reason, Models: key.Models, RemainingQuota: f.balances[org].RemainingQuota, ReadOnly: true}, nil
		}
	}
	return gateway.CenterProbe{}, gateway.CenterHTTPError{Status: 404}
}

func (f *accountGatewayFixture) ExperienceChat(_ context.Context, _ string, keyID int, modelID, _ string) (gateway.CenterChatResult, error) {
	f.chatCalls.Add(1)
	f.chatModel.Store(modelID)
	f.chatKey.Store(int32(keyID))
	if f.chatErr != nil {
		return gateway.CenterChatResult{}, f.chatErr
	}
	quota := 362
	return gateway.CenterChatResult{Response: "真实上游响应", PromptTokens: 12, OutputTokens: 8, TotalTokens: 20, BilledQuota: &quota}, nil
}

func TestAPIKeyCreditAndReadOnlyProbeHTTPFlow(t *testing.T) {
	accounts := newAccountGatewayFixture()
	models := &catalogGateway{models: map[string]struct{}{"deepseek-flash": {}}}
	service := control.NewService(store.NewMemoryBootstrap(), models)
	service.SetGatewayAccounts(accounts)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	model := domain.PublicModel{ID: "deepseek-flash", Name: "DeepSeek Flash", Provider: "DeepSeek", Summary: "Text model", Categories: []string{"文本生成"}}
	createdModel := request(t, server, http.MethodPost, "/api/control/ops/models", model)
	if createdModel.StatusCode != http.StatusCreated {
		t.Fatalf("model create: %d %s", createdModel.StatusCode, readBody(t, createdModel))
	}
	createdModel.Body.Close()
	model.PublicVisible = true
	published := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-flash", model)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("model publish: %d %s", published.StatusCode, readBody(t, published))
	}
	published.Body.Close()

	creditPath := "/api/control/ops/organizations/org_verdantflare/api-credit"
	for _, invalid := range []map[string]any{
		{"requestId": "credit_test_invalid_001", "amountCents": 123},
		{"requestId": "credit_test_invalid_002", "amountFen": 1},
		{"requestId": "credit_test_invalid_003", "amountFen": 730001},
	} {
		response := request(t, server, http.MethodPost, creditPath, invalid)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid CNY grant accepted: %d %s", response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	grant := map[string]any{"requestId": "credit_test_request_001", "amountFen": 898}
	quoteResponse := request(t, server, http.MethodGet, creditPath+"/quote?amountFen=898", nil)
	if quoteResponse.StatusCode != http.StatusOK {
		t.Fatalf("quote: %d %s", quoteResponse.StatusCode, readBody(t, quoteResponse))
	}
	var quote control.APICreditQuote
	decode(t, quoteResponse, &quote)
	quoteResponse.Body.Close()
	if quote.Currency != "CNY" || quote.RequestedFen != 898 || quote.CreditedYuan != "8.979" {
		t.Fatalf("wrong CNY quote: %+v", quote)
	}
	for _, want := range []int{http.StatusOK, http.StatusOK} {
		response := request(t, server, http.MethodPost, creditPath, grant)
		if response.StatusCode != want {
			t.Fatalf("grant: %d %s", response.StatusCode, readBody(t, response))
		}
		var balance control.APICreditBalance
		decode(t, response, &balance)
		response.Body.Close()
		if balance.Currency != "CNY" || balance.RemainingQuota != 615000 || balance.RemainingYuan != "8.979" {
			t.Fatalf("grant was not retry-safe: %+v", balance)
		}
	}
	billingResponse := request(t, server, http.MethodGet, "/api/control/settings/billing", nil)
	if billingResponse.StatusCode != http.StatusOK {
		t.Fatalf("billing: %d %s", billingResponse.StatusCode, readBody(t, billingResponse))
	}
	var billing domain.BillingView
	decode(t, billingResponse, &billing)
	billingResponse.Body.Close()
	if billing.Usage.BudgetQuota != 615000 || billing.Usage.RemainingQuota != 615000 || billing.Usage.UsedQuota != 0 || billing.Usage.BudgetYuan != "8.979" || len(billing.CreditGrants) != 1 || billing.CreditGrants[0].AmountYuan != "8.979" {
		t.Fatalf("billing must use gateway balance and one idempotent grant: %+v", billing)
	}
	accounts.creditErr = gateway.CenterHTTPError{Status: http.StatusInternalServerError}
	billingUnavailable := request(t, server, http.MethodGet, "/api/control/settings/billing", nil)
	if billingUnavailable.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("billing must fail closed without the ledger: %d %s", billingUnavailable.StatusCode, readBody(t, billingUnavailable))
	}
	billingUnavailable.Body.Close()
	accounts.creditErr = nil
	keyPath := "/api/control/api-keys"
	created := request(t, server, http.MethodPost, keyPath, map[string]any{"name": "Production", "scopes": []string{"deepseek-flash"}, "expiresInDays": 30, "requestId": "key_test_request_001"})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create key: %d %s", created.StatusCode, readBody(t, created))
	}
	var secret control.CreateAPIKeyResult
	decode(t, created, &secret)
	created.Body.Close()
	if secret.Secret != "sk-test-secret" {
		t.Fatalf("missing one-time secret")
	}
	listed := request(t, server, http.MethodGet, keyPath, nil)
	var keys []domain.APIKey
	decode(t, listed, &keys)
	listed.Body.Close()
	if len(keys) != 1 || keys[0].Source != "gateway" || keys[0].ID != secret.ID {
		t.Fatalf("unexpected key list: %+v", keys)
	}
	probe := request(t, server, http.MethodGet, keyPath+"/"+secret.ID+"/probe", nil)
	var result control.APIKeyProbe
	decode(t, probe, &result)
	probe.Body.Close()
	if !result.OK || !result.ReadOnly || result.Currency != "CNY" || result.RemainingYuan != "8.979" || fProbeCount(accounts) != 1 {
		t.Fatalf("probe: %+v", result)
	}
	revoked := request(t, server, http.MethodDelete, keyPath+"/"+secret.ID, nil)
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", revoked.StatusCode, readBody(t, revoked))
	}
	revoked.Body.Close()
	listed = request(t, server, http.MethodGet, keyPath, nil)
	decode(t, listed, &keys)
	listed.Body.Close()
	if keys[0].Status != "revoked" {
		t.Fatalf("revoke not reflected in gateway list: %+v", keys[0])
	}
	frozen := request(t, server, http.MethodPatch, "/api/control/ops/organizations/org_verdantflare", map[string]any{
		"plan": "Enterprise", "status": "冻结", "appIds": []string{}, "expectedEntitlementVersion": 1,
	})
	if frozen.StatusCode != http.StatusOK {
		t.Fatalf("freeze: %d %s", frozen.StatusCode, readBody(t, frozen))
	}
	frozen.Body.Close()
	if accounts.balances["org_verdantflare"].Enabled {
		t.Fatal("freeze did not disable gateway account")
	}
	frozenQuote := request(t, server, http.MethodGet, creditPath+"/quote?amountFen=730", nil)
	if frozenQuote.StatusCode != http.StatusForbidden {
		t.Fatalf("frozen organization got grant quote: %d %s", frozenQuote.StatusCode, readBody(t, frozenQuote))
	}
	frozenQuote.Body.Close()
	blocked := request(t, server, http.MethodPost, keyPath, map[string]any{"name": "Blocked", "scopes": []string{"deepseek-flash"}, "expiresInDays": 30, "requestId": "key_test_request_002"})
	if blocked.StatusCode != http.StatusForbidden {
		t.Fatalf("frozen org key creation = %d", blocked.StatusCode)
	}
	blocked.Body.Close()
	for _, version := range []int{2, 3} {
		if version == 3 {
			balance := accounts.balances["org_verdantflare"]
			balance.Enabled = false // Simulate an interrupted prior gateway enable.
			accounts.balances["org_verdantflare"] = balance
		}
		thawed := request(t, server, http.MethodPatch, "/api/control/ops/organizations/org_verdantflare", map[string]any{
			"plan": "Enterprise", "status": "正常", "appIds": []string{}, "expectedEntitlementVersion": version,
		})
		if thawed.StatusCode != http.StatusOK {
			t.Fatalf("thaw/reconcile: %d %s", thawed.StatusCode, readBody(t, thawed))
		}
		thawed.Body.Close()
		if !accounts.balances["org_verdantflare"].Enabled {
			t.Fatal("normal organization did not enable gateway account")
		}
	}
}

func TestAPICreditRoleAndGatewayKeyOrganizationIsolation(t *testing.T) {
	accounts := newAccountGatewayFixture()
	accounts.keys["org_verdantflare"] = []gateway.CenterKey{{ID: 7, Name: "Only first org", Status: "active"}}
	service := control.NewService(store.NewMemorySeeded(time.Now()))
	service.SetGatewayAccounts(accounts)
	server := httptest.NewServer(httpapi.New(testConfig(), service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	switchResponse := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	if switchResponse.StatusCode != http.StatusOK {
		t.Fatalf("switch: %d", switchResponse.StatusCode)
	}
	switchResponse.Body.Close()
	credit := request(t, server, http.MethodPost, "/api/control/ops/organizations/org_northshore/api-credit", map[string]any{"requestId": "credit_test_request_002", "amountFen": 730})
	if credit.StatusCode != http.StatusForbidden {
		t.Fatalf("non-CS admin grant = %d", credit.StatusCode)
	}
	credit.Body.Close()
	quote := request(t, server, http.MethodGet, "/api/control/ops/organizations/org_northshore/api-credit/quote?amountFen=730", nil)
	if quote.StatusCode != http.StatusForbidden {
		t.Fatalf("non-CS admin grant quote = %d", quote.StatusCode)
	}
	quote.Body.Close()
	listed := request(t, server, http.MethodGet, "/api/control/api-keys", nil)
	var keys []domain.APIKey
	decode(t, listed, &keys)
	listed.Body.Close()
	if len(keys) != 0 {
		t.Fatalf("other org keys leaked: %+v", keys)
	}
	probe := request(t, server, http.MethodGet, "/api/control/api-keys/gw_7/probe", nil)
	if probe.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org probe = %d", probe.StatusCode)
	}
	probe.Body.Close()
}

func fProbeCount(f *accountGatewayFixture) int { return f.probes }
