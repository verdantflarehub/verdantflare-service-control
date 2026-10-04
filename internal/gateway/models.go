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

type ModelsClient struct {
	url    string
	token  string
	client *http.Client
}

func NewModelsClient(baseURL, token string) (*ModelsClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("CONTROL_GATEWAY_BASE_URL must be an HTTP(S) origin without a path, query or credentials")
	}
	if token == "" {
		return nil, errors.New("CONTROL_GATEWAY_TOKEN is required")
	}
	return &ModelsClient{
		url:   baseURL + "/v1/models",
		token: token,
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *ModelsClient) ListModels(ctx context.Context) (map[string]struct{}, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("gateway model request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway model request returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil || !payload.Success || payload.Data == nil {
		return nil, errors.New("gateway model response is invalid")
	}
	models := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID != "" {
			models[model.ID] = struct{}{}
		}
	}
	return models, nil
}
