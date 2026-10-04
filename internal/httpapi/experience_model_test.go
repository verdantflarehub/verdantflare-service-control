package httpapi_test

import (
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

func TestModelExperienceHTTPFlow(t *testing.T) {
	accounts := newAccountGatewayFixture()
	service := control.NewService(store.NewMemoryBootstrap(), &catalogGateway{models: map[string]struct{}{"deepseek-flash": {}}})
	service.SetGatewayAccounts(accounts)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	model := domain.PublicModel{ID: "deepseek-flash", Name: "DeepSeek Flash", Provider: "DeepSeek", Summary: "Text model", Categories: []string{"文本生成"}}
	created := request(t, server, http.MethodPost, "/api/control/ops/models", model)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create model: %d %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()
	model.PublicVisible = true
	published := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-flash", model)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish model: %d %s", published.StatusCode, readBody(t, published))
	}
	published.Body.Close()

	path := "/api/control/experience/model-runs"
	input := map[string]string{"requestId": "model_experience_request_001", "modelId": "deepseek-flash", "prompt": "你好"}
	noCredit := request(t, server, http.MethodPost, path, input)
	if noCredit.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("no credit: %d %s", noCredit.StatusCode, readBody(t, noCredit))
	}
	noCredit.Body.Close()
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: true, RemainingQuota: 500000}

	accepted := request(t, server, http.MethodPost, path, input)
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("submit: %d %s", accepted.StatusCode, readBody(t, accepted))
	}
	var run domain.ModelExperienceRun
	decode(t, accepted, &run)
	accepted.Body.Close()
	if run.ID == "" || run.OrganizationID != "org_verdantflare" || run.Status != "submitting" {
		t.Fatalf("bad initial run: %+v", run)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := request(t, server, http.MethodGet, path+"/"+run.ID, nil)
		if result.StatusCode != http.StatusOK {
			t.Fatalf("get run: %d %s", result.StatusCode, readBody(t, result))
		}
		decode(t, result, &run)
		result.Body.Close()
		if run.Status != "submitting" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "completed" || run.Response != "真实上游响应" || run.TotalTokens != 20 {
		t.Fatalf("run did not persist a real gateway result: %+v", run)
	}
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: false, RemainingQuota: 0}
	retry := request(t, server, http.MethodPost, path, input)
	if retry.StatusCode != http.StatusAccepted {
		t.Fatalf("retry: %d %s", retry.StatusCode, readBody(t, retry))
	}
	var same domain.ModelExperienceRun
	decode(t, retry, &same)
	retry.Body.Close()
	if same.ID != run.ID || accounts.chatCalls.Load() != 1 {
		t.Fatalf("retry duplicated a paid call: original=%s retry=%s calls=%d", run.ID, same.ID, accounts.chatCalls.Load())
	}
	listed := request(t, server, http.MethodGet, path, nil)
	var runs []domain.ModelExperienceRun
	decode(t, listed, &runs)
	listed.Body.Close()
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("history was not persisted: %+v", runs)
	}
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: true, RemainingQuota: 500000}
	accounts.chatErr = gateway.CenterHTTPError{Status: http.StatusServiceUnavailable}
	uncertainInput := map[string]string{"requestId": "model_experience_request_002", "modelId": "deepseek-flash", "prompt": "第二次提问"}
	uncertain := request(t, server, http.MethodPost, path, uncertainInput)
	if uncertain.StatusCode != http.StatusAccepted {
		t.Fatalf("uncertain submit: %d %s", uncertain.StatusCode, readBody(t, uncertain))
	}
	var uncertainRun domain.ModelExperienceRun
	decode(t, uncertain, &uncertainRun)
	uncertain.Body.Close()
	deadline = time.Now().Add(2 * time.Second)
	for uncertainRun.Status == "submitting" && time.Now().Before(deadline) {
		result := request(t, server, http.MethodGet, path+"/"+uncertainRun.ID, nil)
		decode(t, result, &uncertainRun)
		result.Body.Close()
		if uncertainRun.Status == "submitting" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if uncertainRun.Status != "outcome_unknown" || uncertainRun.ErrorCode != "upstream_result_unknown" {
		t.Fatalf("ambiguous upstream failure was shown as safely retryable: %+v", uncertainRun)
	}
}
