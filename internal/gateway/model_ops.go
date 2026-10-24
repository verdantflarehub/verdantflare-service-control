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
	ModelID  string          `json:"modelId"`
	Revision string          `json:"revision"`
	Channels json.RawMessage `json:"channels"`
	Pricing  json.RawMessage `json:"pricing"`
	Metadata json.RawMessage `json:"metadata"`
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
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil || !envelope.Success || envelope.Data.ModelID != modelID || len(envelope.Data.Revision) != 64 || len(envelope.Data.Channels) == 0 || len(envelope.Data.Pricing) == 0 {
		return ModelOpsSnapshot{}, errors.New("invalid model operations gateway response")
	}
	return envelope.Data, nil
}
