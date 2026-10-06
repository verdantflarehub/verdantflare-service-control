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
	service := control.NewService(store.NewMemoryBootstrap(), &catalogGateway{models: map[string]struct{}{"deepseek-flash": {}, "deepseek-v4-pro": {}}, chatModels: map[string]struct{}{"deepseek-flash": {}, "deepseek-v4-pro": {}}})
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
	model.ExperienceMode = "chat"
	published := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-flash", model)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish model: %d %s", published.StatusCode, readBody(t, published))
	}
	published.Body.Close()

	path := "/api/control/experience/model-runs"
	accounts.keys["org_verdantflare"] = []gateway.CenterKey{{ID: 7, Name: "my-key", Status: "active", Models: []string{"deepseek-flash", "deepseek-v4-pro"}}}
	input := map[string]string{"requestId": "model_experience_request_001", "modelId": "deepseek-flash", "keyId": "gw_7", "prompt": "你好"}
	withoutKey := request(t, server, http.MethodPost, path, map[string]string{"requestId": "model_experience_request_004", "modelId": "deepseek-flash", "prompt": "你好"})
	if withoutKey.StatusCode != http.StatusBadRequest || accounts.chatCalls.Load() != 0 {
		t.Fatalf("missing key reached paid gateway: %d %s", withoutKey.StatusCode, readBody(t, withoutKey))
	}
	withoutKey.Body.Close()
	noCredit := request(t, server, http.MethodPost, path, input)
	if noCredit.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("no credit: %d %s", noCredit.StatusCode, readBody(t, noCredit))
	}
	noCredit.Body.Close()
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: true, RemainingQuota: 500000}
	accounts.keys["org_verdantflare"][0].Status = "revoked"
	revoked := request(t, server, http.MethodPost, path, input)
	if revoked.StatusCode != http.StatusForbidden || accounts.chatCalls.Load() != 0 {
		t.Fatalf("revoked key reached paid gateway: %d %s", revoked.StatusCode, readBody(t, revoked))
	}
	revoked.Body.Close()
	accounts.keys["org_verdantflare"][0].Status = "active"
	accounts.keys["org_verdantflare"][0].Models = []string{"deepseek-v4-pro"}
	wrongModel := request(t, server, http.MethodPost, path, input)
	if wrongModel.StatusCode != http.StatusForbidden || accounts.chatCalls.Load() != 0 {
		t.Fatalf("out-of-scope model reached paid gateway: %d %s", wrongModel.StatusCode, readBody(t, wrongModel))
	}
	wrongModel.Body.Close()
	accounts.keys["org_verdantflare"][0].Models = []string{"deepseek-flash", "deepseek-v4-pro"}
	otherOrganizationKey := map[string]string{"requestId": input["requestId"], "modelId": input["modelId"], "keyId": "gw_8", "prompt": input["prompt"]}
	missingFromOrganization := request(t, server, http.MethodPost, path, otherOrganizationKey)
	if missingFromOrganization.StatusCode != http.StatusNotFound || accounts.chatCalls.Load() != 0 {
		t.Fatalf("foreign key reached paid gateway: %d %s", missingFromOrganization.StatusCode, readBody(t, missingFromOrganization))
	}
	missingFromOrganization.Body.Close()

	accepted := request(t, server, http.MethodPost, path, input)
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("submit: %d %s", accepted.StatusCode, readBody(t, accepted))
	}
	var run domain.ModelExperienceRun
	decode(t, accepted, &run)
	accepted.Body.Close()
	if run.ID == "" || run.OrganizationID != "org_verdantflare" || run.KeyID != "gw_7" || run.Status != "submitting" {
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
	if run.Status != "completed" || run.Response != "真实上游响应" || run.TotalTokens != 20 || run.BilledQuota == nil || *run.BilledQuota != 362 {
		t.Fatalf("run did not persist a real gateway result: %+v", run)
	}
	if accounts.chatModel.Load() != "deepseek-flash" || accounts.chatKey.Load() != 7 {
		t.Fatalf("gateway received wrong model: %v", accounts.chatModel.Load())
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
	changedKey := request(t, server, http.MethodPost, path, otherOrganizationKey)
	if changedKey.StatusCode != http.StatusConflict || accounts.chatCalls.Load() != 1 {
		t.Fatalf("retry with changed key duplicated a paid call: %d %s", changedKey.StatusCode, readBody(t, changedKey))
	}
	changedKey.Body.Close()
	listed := request(t, server, http.MethodGet, path, nil)
	var runs []domain.ModelExperienceRun
	decode(t, listed, &runs)
	listed.Body.Close()
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("history was not persisted: %+v", runs)
	}
	pro := domain.PublicModel{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Provider: "DeepSeek", Summary: "Text model", Categories: []string{"文本生成"}}
	createdPro := request(t, server, http.MethodPost, "/api/control/ops/models", pro)
	if createdPro.StatusCode != http.StatusCreated {
		t.Fatalf("create second model: %d %s", createdPro.StatusCode, readBody(t, createdPro))
	}
	createdPro.Body.Close()
	pro.ExperienceMode = "chat"
	unpublishedPro := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-v4-pro", pro)
	if unpublishedPro.StatusCode != http.StatusBadRequest {
		t.Fatalf("unpublished model enabled experience: %d %s", unpublishedPro.StatusCode, readBody(t, unpublishedPro))
	}
	unpublishedPro.Body.Close()
	pro.ExperienceMode = ""
	pro.PublicVisible = true
	publishedPro := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-v4-pro", pro)
	if publishedPro.StatusCode != http.StatusOK {
		t.Fatalf("publish second model: %d %s", publishedPro.StatusCode, readBody(t, publishedPro))
	}
	publishedPro.Body.Close()
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: true, RemainingQuota: 500000}
	proInput := map[string]string{"requestId": "model_experience_request_003", "modelId": pro.ID, "keyId": "gw_7", "prompt": "介绍一下自己"}
	disabled := request(t, server, http.MethodPost, path, proInput)
	if disabled.StatusCode != http.StatusForbidden || accounts.chatCalls.Load() != 1 {
		t.Fatalf("closed experience reached gateway: %d calls=%d %s", disabled.StatusCode, accounts.chatCalls.Load(), readBody(t, disabled))
	}
	disabled.Body.Close()
	pro.ExperienceMode = "chat"
	enabledPro := request(t, server, http.MethodPatch, "/api/control/ops/models/deepseek-v4-pro", pro)
	if enabledPro.StatusCode != http.StatusOK {
		t.Fatalf("enable second model: %d %s", enabledPro.StatusCode, readBody(t, enabledPro))
	}
	enabledPro.Body.Close()
	proRunResponse := request(t, server, http.MethodPost, path, proInput)
	if proRunResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("submit second model: %d %s", proRunResponse.StatusCode, readBody(t, proRunResponse))
	}
	var proRun domain.ModelExperienceRun
	decode(t, proRunResponse, &proRun)
	proRunResponse.Body.Close()
	deadline = time.Now().Add(2 * time.Second)
	for proRun.Status == "submitting" && time.Now().Before(deadline) {
		result := request(t, server, http.MethodGet, path+"/"+proRun.ID, nil)
		decode(t, result, &proRun)
		result.Body.Close()
		if proRun.Status == "submitting" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if proRun.Status != "completed" || proRun.ModelID != pro.ID || accounts.chatModel.Load() != pro.ID {
		t.Fatalf("second model did not reach gateway unchanged: %+v gateway=%v", proRun, accounts.chatModel.Load())
	}
	accounts.balances["org_verdantflare"] = gateway.CenterBalance{Enabled: true, RemainingQuota: 500000}
	accounts.chatErr = gateway.CenterHTTPError{Status: http.StatusServiceUnavailable}
	uncertainInput := map[string]string{"requestId": "model_experience_request_002", "modelId": "deepseek-flash", "keyId": "gw_7", "prompt": "第二次提问"}
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
