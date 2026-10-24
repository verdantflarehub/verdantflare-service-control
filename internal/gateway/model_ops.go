package gateway

import (
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
	Channels []ModelOpsChannel `json:"channels"`
	Pricing  ModelOpsPricing   `json:"pricing"`
	Metadata ModelOpsMetadata  `json:"metadata"`
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
	Status      int    `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
	Endpoints   string `json:"endpoints,omitempty"`
}

type ModelOpsClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewModelOpsClient(baseURL, token string) (*ModelOpsClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || len(token) < 32 {
		return nil, errors.New("invalid model operations gateway origin or credential")
	}
	return &ModelOpsClient{baseURL: baseURL + "/api/internal/center/model-ops", token: token,
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
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
