package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelOpsClientSnapshot(t *testing.T) {
	const serviceToken = "model-ops-service-token-at-least-thirty-two-characters"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/center/model-ops/snapshot" || r.URL.Query().Get("modelId") != "minimax/h3" || r.Header.Get("Authorization") != "Bearer "+serviceToken {
			t.Errorf("unexpected model ops request: path=%q model=%q authorization=%t", r.URL.Path, r.URL.Query().Get("modelId"), r.Header.Get("Authorization") == "Bearer "+serviceToken)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"modelId":"minimax/h3","revision":"` + strings.Repeat("a", 64) + `","channels":[{"id":1,"name":"safe","key":"should-not-forward","credentialConfigured":true}],"pricing":{"billingMode":"ratio","supplierPassword":"should-not-forward"},"metadata":{"exists":true,"status":0}}}`))
	}))
	defer server.Close()
	client, err := NewModelOpsClient(server.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background(), "minimax/h3")
	if err != nil || snapshot.ModelID != "minimax/h3" || len(snapshot.Revision) != 64 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
	forwarded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(forwarded), "should-not-forward") {
		t.Fatalf("snapshot forwarded unexpected gateway fields: %s, %v", forwarded, err)
	}
	if !strings.Contains(string(forwarded), `"status":0`) {
		t.Fatalf("disabled model status was lost: %s", forwarded)
	}
}

func TestModelOpsClientRejectsInvalidOriginAndCredential(t *testing.T) {
	for _, origin := range []string{"https://user:password@example.test", "https://example.test/path", "https://example.test?x=1"} {
		if _, err := NewModelOpsClient(origin, strings.Repeat("x", 32)); err == nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	if _, err := NewModelOpsClient("https://example.test", "short"); err == nil {
		t.Fatal("accepted short service token")
	}
}

func TestModelOpsClientScopedWriteReadbackDoesNotForwardUnknownFields(t *testing.T) {
	const modelID = "internal-deepseek-test"
	const serviceToken = "model-ops-service-token-at-least-thirty-two-characters"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+serviceToken {
			t.Errorf("unexpected write method or auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/internal/center/model-ops/billing/apply" {
			t.Errorf("unexpected write path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"modelId":"internal-deepseek-test","revision":"` + strings.Repeat("b", 64) + `","pricing":{"billingMode":"ratio","modelPrice":0.02,"supplierKey":"never-forward"},"replayed":false,"supplierKey":"never-forward"}}`))
	}))
	defer server.Close()
	client, err := NewModelOpsClient(server.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	price := 0.02
	result, err := client.BillingApply(context.Background(), ModelOpsBillingInput{ModelID: modelID, Mode: "per_request", PriceUSD: &price})
	if err != nil || result.ModelID != modelID || result.Pricing.ModelPrice == nil || *result.Pricing.ModelPrice != price {
		t.Fatalf("write result = %+v, err = %v", result, err)
	}
	forwarded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(forwarded), "never-forward") || strings.Contains(string(forwarded), "supplierKey") {
		t.Fatalf("write readback forwarded unknown data: %s, %v", forwarded, err)
	}
}

func TestModelOpsProbeRequiresSettledChargeAndDoesNotReturnProviderText(t *testing.T) {
	const serviceToken = "model-ops-service-token-at-least-thirty-two-characters"
	const requestID = "probe_request_0001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+serviceToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/internal/center/model-ops/probe":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"private provider response"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`))
		case "/api/internal/center/model-ops/probe/charges/" + requestID:
			_, _ = w.Write([]byte(`{"success":true,"data":{"requestId":"probe_request_0001","modelId":"internal-test","billedQuota":500,"billedUsd":0.001,"maxUsd":0.01,"verified":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewModelOpsClient(server.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Probe(context.Background(), ModelOpsProbeInput{ModelID: "internal-test", RequestID: requestID,
		ExpectedGatewayRevision: strings.Repeat("a", 64), MaxUSD: 0.01})
	if err != nil || !receipt.Verified || receipt.BilledUSD != 0.001 {
		t.Fatalf("receipt = %+v, err = %v", receipt, err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || strings.Contains(string(encoded), "private provider response") {
		t.Fatalf("probe leaked response: %s, err = %v", encoded, err)
	}
}

func TestModelOpsStageAddsServerSideCatalogTokenOnly(t *testing.T) {
	const serviceToken = "model-ops-service-token-at-least-thirty-two-characters"
	const catalogToken = "sk-catalog-test-token-never-in-mcp-arguments"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/center/model-ops/channel/stage-public" || r.Header.Get("Authorization") != "Bearer "+serviceToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			CatalogToken string `json:"catalogToken"`
			ModelID      string `json:"modelId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.CatalogToken != catalogToken || request.ModelID != "internal-test" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"modelId":"internal-test","sourceChannelId":3,"publicChannelId":4,"revision":"` + strings.Repeat("c", 64) + `","channels":[{"id":4}]}}`))
	}))
	defer server.Close()
	client, err := NewModelOpsClient(server.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StagePublicRoute(context.Background(), ModelOpsPublicRouteInput{ModelID: "internal-test", SourceChannelID: 3}); err == nil {
		t.Fatal("staged route without configured catalog token")
	}
	client.SetCatalogToken(catalogToken)
	result, err := client.StagePublicRoute(context.Background(), ModelOpsPublicRouteInput{ModelID: "internal-test", SourceChannelID: 3})
	if err != nil || result.PublicChannelID != 4 {
		t.Fatalf("stage result = %+v, err = %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), catalogToken) {
		t.Fatalf("stage result exposed catalog token: %v", err)
	}
}
