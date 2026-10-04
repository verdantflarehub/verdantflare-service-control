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
	"strconv"
	"strings"
	"time"
)

type CenterClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type CenterHTTPError struct{ Status int }

func (e CenterHTTPError) Error() string {
	return fmt.Sprintf("center gateway returned HTTP %d", e.Status)
}

type CenterBalance struct {
	RemainingQuota int  `json:"remainingQuota"`
	UsedQuota      int  `json:"usedQuota"`
	Enabled        bool `json:"enabled"`
}

type CenterKey struct {
	ID         int      `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Models     []string `json:"models"`
	CreatedAt  int64    `json:"createdAt"`
	ExpiresAt  int64    `json:"expiresAt"`
	LastUsedAt int64    `json:"lastUsedAt"`
	Status     string   `json:"status"`
}

type CenterCreatedKey struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Secret    string   `json:"secret"`
	Models    []string `json:"models"`
	CreatedAt int64    `json:"createdAt"`
	ExpiresAt int64    `json:"expiresAt"`
}

type CenterProbe struct {
	OK             bool     `json:"ok"`
	Reason         string   `json:"reason"`
	Models         []string `json:"models"`
	RemainingQuota int      `json:"remainingQuota"`
	ReadOnly       bool     `json:"readOnly"`
}

func NewCenterClient(baseURL, token string) (*CenterClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || len(token) < 32 {
		return nil, errors.New("invalid center gateway origin or credential")
	}
	return &CenterClient{baseURL: baseURL + "/api/internal/center/organizations", token: token,
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *CenterClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("center gateway request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return CenterHTTPError{Status: response.StatusCode}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil || !envelope.Success {
		return errors.New("invalid center gateway response")
	}
	if output != nil && len(envelope.Data) > 0 {
		return json.Unmarshal(envelope.Data, output)
	}
	return nil
}

func centerPath(organizationID string) string { return "/" + url.PathEscape(organizationID) }

func (c *CenterClient) Balance(ctx context.Context, organizationID string) (CenterBalance, error) {
	var result CenterBalance
	err := c.request(ctx, http.MethodGet, centerPath(organizationID), nil, &result)
	return result, err
}

func (c *CenterClient) Grant(ctx context.Context, organizationID, requestID, actor string, amountCents int) (CenterBalance, error) {
	var result CenterBalance
	err := c.request(ctx, http.MethodPost, centerPath(organizationID)+"/credit-grants", map[string]any{
		"requestId": requestID, "actor": actor, "amountCents": amountCents,
	}, &result)
	return result, err
}

func (c *CenterClient) SetEnabled(ctx context.Context, organizationID string, enabled bool) error {
	return c.request(ctx, http.MethodPut, centerPath(organizationID)+"/status", map[string]bool{"enabled": enabled}, nil)
}

func (c *CenterClient) ListKeys(ctx context.Context, organizationID string) ([]CenterKey, error) {
	var result []CenterKey
	err := c.request(ctx, http.MethodGet, centerPath(organizationID)+"/keys", nil, &result)
	return result, err
}

func (c *CenterClient) CreateKey(ctx context.Context, organizationID, requestID, name string, models []string, expiresInDays int) (CenterCreatedKey, error) {
	var result CenterCreatedKey
	err := c.request(ctx, http.MethodPost, centerPath(organizationID)+"/keys", map[string]any{
		"requestId": requestID, "name": name, "models": models, "expiresInDays": expiresInDays,
	}, &result)
	return result, err
}

func (c *CenterClient) RevokeKey(ctx context.Context, organizationID string, tokenID int) error {
	return c.request(ctx, http.MethodPost, centerPath(organizationID)+"/keys/"+strconv.Itoa(tokenID)+"/revoke", nil, nil)
}

func (c *CenterClient) ProbeKey(ctx context.Context, organizationID string, tokenID int) (CenterProbe, error) {
	var result CenterProbe
	err := c.request(ctx, http.MethodGet, centerPath(organizationID)+"/keys/"+strconv.Itoa(tokenID)+"/probe", nil, &result)
	return result, err
}
