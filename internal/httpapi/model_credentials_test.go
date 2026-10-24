package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type credentialGateway struct {
	credential string
	calls      int
}

func (*credentialGateway) Snapshot(_ context.Context, modelID string) (gateway.ModelOpsSnapshot, error) {
	return gateway.ModelOpsSnapshot{ModelID: modelID, Revision: strings.Repeat("a", 64)}, nil
}
func (*credentialGateway) BillingPreview(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsBillingPreview, error) {
	return gateway.ModelOpsBillingPreview{}, nil
}
func (*credentialGateway) BillingApply(context.Context, gateway.ModelOpsBillingInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}
func (*credentialGateway) AttachAlias(context.Context, gateway.ModelOpsAliasInput) (gateway.ModelOpsWriteResult, error) {
	return gateway.ModelOpsWriteResult{}, nil
}
func (g *credentialGateway) CreateChannel(_ context.Context, input gateway.ModelOpsChannelCreateInput) (gateway.ModelOpsChannelCreateResult, error) {
	g.calls++
	g.credential = input.Credential
	return gateway.ModelOpsChannelCreateResult{ModelID: input.ModelID, ChannelID: 17, Revision: strings.Repeat("b", 64)}, nil
}

func TestModelCredentialUploadIsBearerBoundSingleUseAndRedacted(t *testing.T) {
	repository := store.NewMemorySeeded(time.Now().UTC())
	service := control.NewService(repository)
	fake := &credentialGateway{}
	service.SetModelOpsGateway(fake)
	server := httptest.NewServer(httpapi.New(testConfig(), service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	created := request(t, server, http.MethodPost, "/api/control/settings/mcp-tokens", map[string]any{
		"name": "模型凭据测试", "purpose": "model_publish", "expiresInDays": 1,
	})
	require.Equal(t, http.StatusCreated, created.StatusCode)
	var token struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&token))
	require.NoError(t, created.Body.Close())
	failed, raw := hubMCPCall(t, server, token.Secret, "model_publish.credentials.prepare", map[string]any{
		"modelId": "internal-deepseek-test", "upstreamModelId": "deepseek-chat", "channelName": "internal-deepseek",
		"requestId": "credential_test_request_001", "expectedGatewayRevision": strings.Repeat("a", 64),
		"credential": "must-not-enter-mcp-arguments",
	})
	require.True(t, failed)
	assert.Contains(t, string(raw), "MODEL_ARGUMENTS_INVALID")
	failed, raw = hubMCPCall(t, server, token.Secret, "model_publish.credentials.prepare", map[string]any{
		"modelId": "internal-deepseek-test", "upstreamModelId": "deepseek-chat", "channelName": "internal-deepseek",
		"requestId": "credential_test_request_001", "expectedGatewayRevision": strings.Repeat("a", 64),
	})
	require.False(t, failed, string(raw))
	var prepared control.ModelCredentialPreparation
	require.NoError(t, json.Unmarshal(raw, &prepared))
	require.NotEmpty(t, prepared.Handle)
	const supplierKey = "sk-secret-never-expose-123456"
	upload := func(bearer string) *http.Response {
		req, err := http.NewRequest(http.MethodPut, server.URL+prepared.UploadPath, bytes.NewBufferString(supplierKey))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-Forwarded-Proto", "https")
		response, err := server.Client().Do(req)
		require.NoError(t, err)
		return response
	}
	_, appToken := issueMCPToken(t, server)
	wrong := upload(appToken)
	assert.Equal(t, http.StatusNotFound, wrong.StatusCode)
	require.NoError(t, wrong.Body.Close())
	accepted := upload(token.Secret)
	require.Equal(t, http.StatusOK, accepted.StatusCode)
	body, err := io.ReadAll(accepted.Body)
	require.NoError(t, err)
	require.NoError(t, accepted.Body.Close())
	assert.NotContains(t, string(body), supplierKey)
	assert.Equal(t, supplierKey, fake.credential)
	assert.Equal(t, 1, fake.calls)
	replay := upload(token.Secret)
	assert.Equal(t, http.StatusConflict, replay.StatusCode)
	require.NoError(t, replay.Body.Close())
	assert.Equal(t, 1, fake.calls)
	failed, raw = hubMCPCall(t, server, token.Secret, "model_publish.credentials.status", map[string]any{"handle": prepared.Handle})
	require.False(t, failed, string(raw))
	assert.Contains(t, string(raw), `"status":"applied"`)
	assert.NotContains(t, string(raw), supplierKey)
}
