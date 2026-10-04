package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	balances map[string]gateway.CenterBalance
	keys     map[string][]gateway.CenterKey
	grants   map[string]bool
	probes   int
}

func newAccountGatewayFixture() *accountGatewayFixture {
	return &accountGatewayFixture{balances: map[string]gateway.CenterBalance{}, keys: map[string][]gateway.CenterKey{}, grants: map[string]bool{}}
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
	}
	return f.balances[org], nil
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
			return gateway.CenterProbe{OK: key.Status == "active" && f.balances[org].RemainingQuota > 0, Reason: key.Status, Models: key.Models, RemainingQuota: f.balances[org].RemainingQuota, ReadOnly: true}, nil
		}
	}
	return gateway.CenterProbe{}, gateway.CenterHTTPError{Status: 404}
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
	grant := map[string]any{"requestId": "credit_test_request_001", "amountCents": 123}
	for _, want := range []int{http.StatusOK, http.StatusOK} {
		response := request(t, server, http.MethodPost, creditPath, grant)
		if response.StatusCode != want {
			t.Fatalf("grant: %d %s", response.StatusCode, readBody(t, response))
		}
		var balance gateway.CenterBalance
		decode(t, response, &balance)
		response.Body.Close()
		if balance.RemainingQuota != 615000 {
			t.Fatalf("grant was not retry-safe: %+v", balance)
		}
	}
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
	var result gateway.CenterProbe
	decode(t, probe, &result)
	probe.Body.Close()
	if !result.OK || !result.ReadOnly || fProbeCount(accounts) != 1 {
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
	credit := request(t, server, http.MethodPost, "/api/control/ops/organizations/org_northshore/api-credit", map[string]any{"requestId": "credit_test_request_002", "amountCents": 100})
	if credit.StatusCode != http.StatusForbidden {
		t.Fatalf("non-CS admin grant = %d", credit.StatusCode)
	}
	credit.Body.Close()
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
