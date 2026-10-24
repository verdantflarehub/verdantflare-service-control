package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ModelOpsSnapshot is the credential-free model configuration readback from
// new-api. Pricing remains a gateway-owned object; Control never computes it.
type ModelOpsSnapshot struct {
	ModelID  string            `json:"modelId"`
	Revision string            `json:"revision"`
	Sequence uint64            `json:"sequence"`
	Channels []ModelOpsChannel `json:"channels"`
	Pricing  ModelOpsPricing   `json:"pricing"`
	Metadata ModelOpsMetadata  `json:"metadata"`
}

// ModelOpsBillingInput is a model-scoped rule. No supplier credential or
// arbitrary new-api option key can be sent through this client.
type ModelOpsBillingInput struct {
	ModelID                 string   `json:"modelId"`
	RequestID               string   `json:"requestId,omitempty"`
	ExpectedGatewayRevision string   `json:"expectedGatewayRevision,omitempty"`
	Mode                    string   `json:"mode"`
	BillingExpr             string   `json:"billingExpr,omitempty"`
	PriceUSD                *float64 `json:"priceUsd,omitempty"`
}

type ModelOpsAliasInput struct {
	ModelID                 string `json:"modelId"`
	SourceModelID           string `json:"sourceModelId"`
	SourceChannelID         int    `json:"sourceChannelId"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

// ModelOpsChannelCreateInput is used only by Control's raw credential upload
// handler. Credential must never be serialized into MCP tool arguments.
type ModelOpsChannelCreateInput struct {
	ModelID                 string `json:"modelId"`
	UpstreamModelID         string `json:"upstreamModelId"`
	Name                    string `json:"name"`
	Credential              string `json:"credential"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsChannelCreateResult struct {
	ModelID   string            `json:"modelId"`
	ChannelID int               `json:"channelId"`
	Revision  string            `json:"revision"`
	Channels  []ModelOpsChannel `json:"channels"`
	Replayed  bool              `json:"replayed"`
}

type ModelOpsChannelUpdateInput struct {
	ModelID                 string `json:"modelId"`
	ChannelID               int    `json:"channelId"`
	Name                    string `json:"name"`
	UpstreamModelID         string `json:"upstreamModelId"`
	Weight                  int    `json:"weight"`
	Priority                int64  `json:"priority"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsGroupPreview struct {
	Group                 string `json:"group"`
	Ratio                 int    `json:"ratio"`
	Exists                bool   `json:"exists"`
	ExpectedGroupRevision string `json:"expectedGroupRevision"`
	Impact                string `json:"impact"`
}

type ModelOpsGroupApplyInput struct {
	Group                 string `json:"group"`
	RequestID             string `json:"requestId"`
	ExpectedGroupRevision string `json:"expectedGroupRevision"`
}

type ModelOpsGroupResult struct {
	Group    string `json:"group"`
	Ratio    int    `json:"ratio"`
	Revision string `json:"revision"`
	Replayed bool   `json:"replayed"`
}

type ModelOpsMetadataInput struct {
	ModelID                 string `json:"modelId"`
	Description             string `json:"description"`
	Endpoints               string `json:"endpoints"`
	Status                  int    `json:"status"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsMetadataResult struct {
	ModelID  string           `json:"modelId"`
	Revision string           `json:"revision"`
	Metadata ModelOpsMetadata `json:"metadata"`
	Replayed bool             `json:"replayed"`
}

type ModelOpsChannelDisableInput struct {
	ModelID                 string `json:"modelId"`
	ChannelID               int    `json:"channelId"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsCredentialRotateInput struct {
	ModelID                 string `json:"modelId"`
	ChannelID               int    `json:"channelId"`
	Credential              string `json:"credential"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsProbeInput struct {
	ModelID                 string  `json:"modelId"`
	RequestID               string  `json:"requestId"`
	ExpectedGatewayRevision string  `json:"expectedGatewayRevision"`
	MaxUSD                  float64 `json:"maxUsd"`
}

type ModelOpsProbeCharge struct {
	RequestID       string  `json:"requestId"`
	ModelID         string  `json:"modelId"`
	GatewayRevision string  `json:"gatewayRevision"`
	BilledQuota     int     `json:"billedQuota"`
	BilledUSD       float64 `json:"billedUsd"`
	MaxUSD          float64 `json:"maxUsd"`
	Verified        bool    `json:"verified"`
}

type ModelOpsProbeReceipt struct {
	ModelOpsProbeCharge
	GatewayRevision string `json:"gatewayRevision"`
	PromptTokens    int    `json:"promptTokens"`
	OutputTokens    int    `json:"outputTokens"`
}

type ModelOpsPublicRouteInput struct {
	ModelID                 string `json:"modelId"`
	SourceChannelID         int    `json:"sourceChannelId"`
	PublicChannelID         int    `json:"publicChannelId,omitempty"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelOpsPublicRouteResult struct {
	ModelID         string            `json:"modelId"`
	SourceChannelID int               `json:"sourceChannelId"`
	PublicChannelID int               `json:"publicChannelId"`
	Revision        string            `json:"revision"`
	Channels        []ModelOpsChannel `json:"channels"`
	Replayed        bool              `json:"replayed"`
}

type ModelOpsWriteResult struct {
	ModelID  string            `json:"modelId"`
	Revision string            `json:"revision"`
	Pricing  ModelOpsPricing   `json:"pricing,omitempty"`
	Channels []ModelOpsChannel `json:"channels,omitempty"`
	Replayed bool              `json:"replayed"`
}

type ModelOpsBillingPreview struct {
	ModelID              string   `json:"modelId"`
	CurrentRevision      string   `json:"currentRevision"`
	Mode                 string   `json:"mode"`
	PriceUSD             *float64 `json:"priceUsd,omitempty"`
	Validated            bool     `json:"validated"`
	ActualChargeVerified bool     `json:"actualChargeVerified"`
}

// Explicit response types are an allowlist: if the gateway later adds a
// credential-bearing field, Control will not forward it to MCP callers.
type ModelOpsChannel struct {
	ID                   int      `json:"id"`
	Name                 string   `json:"name"`
	Type                 int      `json:"type"`
	Status               int      `json:"status"`
	Groups               []string `json:"groups"`
	UpstreamModel        string   `json:"upstreamModel"`
	UpstreamHost         string   `json:"upstreamHost,omitempty"`
	CredentialConfigured bool     `json:"credentialConfigured"`
	Weight               int      `json:"weight"`
	Priority             int64    `json:"priority"`
}

type ModelOpsPricing struct {
	BillingMode          string             `json:"billingMode"`
	BillingExpr          string             `json:"billingExpr,omitempty"`
	ModelPrice           *float64           `json:"modelPrice,omitempty"`
	ModelRatio           *float64           `json:"modelRatio,omitempty"`
	CompletionRatio      *float64           `json:"completionRatio,omitempty"`
	CacheRatio           *float64           `json:"cacheRatio,omitempty"`
	CreateCacheRatio     *float64           `json:"createCacheRatio,omitempty"`
	ImageRatio           *float64           `json:"imageRatio,omitempty"`
	AudioRatio           *float64           `json:"audioRatio,omitempty"`
	AudioCompletionRatio *float64           `json:"audioCompletionRatio,omitempty"`
	GroupRatios          map[string]float64 `json:"groupRatios"`
}

type ModelOpsMetadata struct {
	Exists      bool   `json:"exists"`
	Status      int    `json:"status"`
	Description string `json:"description,omitempty"`
	Endpoints   string `json:"endpoints,omitempty"`
}

type ModelOpsClient struct {
	baseURL      string
	token        string
	catalogToken string
	client       *http.Client
	probeClient  *http.Client
}

// SetCatalogToken binds the existing read-only directory token to the
// server-to-server staging call. Its secret never appears in MCP arguments.
func (c *ModelOpsClient) SetCatalogToken(token string) { c.catalogToken = strings.TrimSpace(token) }

func NewModelOpsClient(baseURL, token string) (*ModelOpsClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || len(token) < 32 {
		return nil, errors.New("invalid model operations gateway origin or credential")
	}
	return &ModelOpsClient{baseURL: baseURL + "/api/internal/center/model-ops", token: token,
		client:      &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		probeClient: &http.Client{Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *ModelOpsClient) Snapshot(ctx context.Context, modelID string) (ModelOpsSnapshot, error) {
	if strings.TrimSpace(modelID) == "" || len(modelID) > 128 {
		return ModelOpsSnapshot{}, errors.New("invalid model ID")
	}
	endpoint := c.baseURL + "/snapshot?modelId=" + url.QueryEscape(modelID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelOpsSnapshot{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return ModelOpsSnapshot{}, fmt.Errorf("model operations gateway unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelOpsSnapshot{}, CenterHTTPError{Status: response.StatusCode}
	}
	var envelope struct {
		Success bool             `json:"success"`
		Data    ModelOpsSnapshot `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil || !envelope.Success || envelope.Data.ModelID != modelID || len(envelope.Data.Revision) != 64 || envelope.Data.Channels == nil || envelope.Data.Pricing.BillingMode == "" {
		return ModelOpsSnapshot{}, errors.New("invalid model operations gateway response")
	}
	return envelope.Data, nil
}

func (c *ModelOpsClient) post(ctx context.Context, path string, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("model operations gateway unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CenterHTTPError{Status: response.StatusCode}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil || !envelope.Success || len(envelope.Data) == 0 {
		return errors.New("invalid model operations gateway response")
	}
	return json.Unmarshal(envelope.Data, output)
}

func (c *ModelOpsClient) BillingPreview(ctx context.Context, input ModelOpsBillingInput) (ModelOpsBillingPreview, error) {
	var result ModelOpsBillingPreview
	err := c.post(ctx, "/billing/preview", input, &result)
	if err == nil && (result.ModelID != input.ModelID || !result.Validated || result.ActualChargeVerified) {
		err = errors.New("invalid model billing preview")
	}
	return result, err
}

func (c *ModelOpsClient) BillingApply(ctx context.Context, input ModelOpsBillingInput) (ModelOpsWriteResult, error) {
	var result ModelOpsWriteResult
	err := c.post(ctx, "/billing/apply", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || result.Pricing.BillingMode == "") {
		err = errors.New("invalid model billing readback")
	}
	return result, err
}

func (c *ModelOpsClient) AttachAlias(ctx context.Context, input ModelOpsAliasInput) (ModelOpsWriteResult, error) {
	var result ModelOpsWriteResult
	err := c.post(ctx, "/channel/attach-alias", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid model channel readback")
	}
	return result, err
}

func (c *ModelOpsClient) CreateChannel(ctx context.Context, input ModelOpsChannelCreateInput) (ModelOpsChannelCreateResult, error) {
	var result ModelOpsChannelCreateResult
	err := c.post(ctx, "/channel/create", input, &result)
	if err == nil && (result.ModelID != input.ModelID || result.ChannelID <= 0 || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid model channel readback")
	}
	return result, err
}

func (c *ModelOpsClient) UpdateChannel(ctx context.Context, input ModelOpsChannelUpdateInput) (ModelOpsWriteResult, error) {
	var result ModelOpsWriteResult
	err := c.post(ctx, "/channel/update", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid channel update readback")
	}
	return result, err
}

func (c *ModelOpsClient) GroupPreview(ctx context.Context) (ModelOpsGroupPreview, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/group/preview", nil)
	if err != nil {
		return ModelOpsGroupPreview{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return ModelOpsGroupPreview{}, fmt.Errorf("model group gateway unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelOpsGroupPreview{}, CenterHTTPError{Status: response.StatusCode}
	}
	var envelope struct {
		Success bool                 `json:"success"`
		Data    ModelOpsGroupPreview `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&envelope); err != nil || !envelope.Success || envelope.Data.Group != "center-internal" || len(envelope.Data.ExpectedGroupRevision) != 64 {
		return ModelOpsGroupPreview{}, errors.New("invalid model group preview")
	}
	return envelope.Data, nil
}

func (c *ModelOpsClient) GroupApply(ctx context.Context, input ModelOpsGroupApplyInput) (ModelOpsGroupResult, error) {
	var result ModelOpsGroupResult
	err := c.post(ctx, "/group/apply", input, &result)
	if err == nil && (result.Group != "center-internal" || len(result.Revision) != 64 || result.Ratio != 1) {
		err = errors.New("invalid model group readback")
	}
	return result, err
}

func (c *ModelOpsClient) ConfigureMetadata(ctx context.Context, input ModelOpsMetadataInput) (ModelOpsMetadataResult, error) {
	var result ModelOpsMetadataResult
	err := c.post(ctx, "/gateway-model/configure", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || !result.Metadata.Exists || result.Metadata.Status != input.Status) {
		err = errors.New("invalid model metadata readback")
	}
	return result, err
}

func (c *ModelOpsClient) DisableChannel(ctx context.Context, input ModelOpsChannelDisableInput) (ModelOpsWriteResult, error) {
	var result ModelOpsWriteResult
	err := c.post(ctx, "/channel/disable", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid channel disable readback")
	}
	return result, err
}

func (c *ModelOpsClient) RotateCredential(ctx context.Context, input ModelOpsCredentialRotateInput) (ModelOpsWriteResult, error) {
	var result ModelOpsWriteResult
	err := c.post(ctx, "/credentials/rotate", input, &result)
	if err == nil && (result.ModelID != input.ModelID || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid credential rotation readback")
	}
	return result, err
}

func (c *ModelOpsClient) StagePublicRoute(ctx context.Context, input ModelOpsPublicRouteInput) (ModelOpsPublicRouteResult, error) {
	var result ModelOpsPublicRouteResult
	if !strings.HasPrefix(c.catalogToken, "sk-") {
		return result, errors.New("catalog token is not configured for model publication")
	}
	err := c.post(ctx, "/channel/stage-public", struct {
		ModelOpsPublicRouteInput
		CatalogToken string `json:"catalogToken"`
	}{input, c.catalogToken}, &result)
	if err == nil && (result.ModelID != input.ModelID || result.SourceChannelID != input.SourceChannelID ||
		result.PublicChannelID <= 0 || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid staged public route readback")
	}
	return result, err
}

func (c *ModelOpsClient) ActivatePublicRoute(ctx context.Context, input ModelOpsPublicRouteInput) (ModelOpsPublicRouteResult, error) {
	var result ModelOpsPublicRouteResult
	err := c.post(ctx, "/channel/activate-public", input, &result)
	if err == nil && (result.ModelID != input.ModelID || result.SourceChannelID != input.SourceChannelID ||
		result.PublicChannelID != input.PublicChannelID || len(result.Revision) != 64 || len(result.Channels) == 0) {
		err = errors.New("invalid activated public route readback")
	}
	return result, err
}

func (c *ModelOpsClient) ProbeCharge(ctx context.Context, requestID string) (ModelOpsProbeCharge, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/probe/charges/"+url.PathEscape(requestID), nil)
	if err != nil {
		return ModelOpsProbeCharge{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return ModelOpsProbeCharge{}, fmt.Errorf("model probe charge unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelOpsProbeCharge{}, CenterHTTPError{Status: response.StatusCode}
	}
	var envelope struct {
		Success bool                `json:"success"`
		Data    ModelOpsProbeCharge `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&envelope); err != nil || !envelope.Success || envelope.Data.RequestID != requestID || !envelope.Data.Verified || envelope.Data.BilledQuota <= 0 || envelope.Data.BilledUSD > 0.01 {
		return ModelOpsProbeCharge{}, errors.New("invalid model probe charge readback")
	}
	return envelope.Data, nil
}

func (c *ModelOpsClient) Probe(ctx context.Context, input ModelOpsProbeInput) (ModelOpsProbeReceipt, error) {
	encoded, err := json.Marshal(map[string]string{"modelId": input.ModelID, "requestId": input.RequestID,
		"expectedGatewayRevision": input.ExpectedGatewayRevision})
	if err != nil {
		return ModelOpsProbeReceipt{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/probe", bytes.NewReader(encoded))
	if err != nil {
		return ModelOpsProbeReceipt{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.probeClient.Do(request)
	if err != nil {
		return ModelOpsProbeReceipt{}, fmt.Errorf("model probe outcome unknown; check charge by request ID: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelOpsProbeReceipt{}, CenterHTTPError{Status: response.StatusCode}
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			OutputTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil || len(result.Choices) == 0 || result.Usage.PromptTokens <= 0 || result.Usage.OutputTokens < 0 {
		return ModelOpsProbeReceipt{}, errors.New("model probe response invalid; check charge by request ID")
	}
	charge, err := c.ProbeCharge(ctx, input.RequestID)
	if err != nil || charge.ModelID != input.ModelID || charge.GatewayRevision != input.ExpectedGatewayRevision {
		return ModelOpsProbeReceipt{}, errors.New("model probe charge not verified; check charge by request ID")
	}
	return ModelOpsProbeReceipt{ModelOpsProbeCharge: charge, GatewayRevision: input.ExpectedGatewayRevision,
		PromptTokens: result.Usage.PromptTokens, OutputTokens: result.Usage.OutputTokens}, nil
}
