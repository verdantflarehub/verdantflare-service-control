package gateway

import (
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

type LoginDirectoryUser struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Status        string    `json:"status"`
	EmailVerified bool      `json:"emailVerified"`
	CreatedAt     time.Time `json:"createdAt"`
}

type LoginDirectoryPage struct {
	Users      []LoginDirectoryUser `json:"users"`
	NextCursor string               `json:"nextCursor"`
}

type LoginDirectoryClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type LoginDirectoryHTTPError struct{ Status int }

func (e LoginDirectoryHTTPError) Error() string {
	return fmt.Sprintf("Login directory returned HTTP %d", e.Status)
}

func NewLoginDirectoryClient(baseURL, token string) (*LoginDirectoryClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || len(token) < 32 {
		return nil, errors.New("invalid Login directory origin or credential")
	}
	return &LoginDirectoryClient{baseURL: strings.TrimRight(baseURL, "/"), token: token,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *LoginDirectoryClient) get(ctx context.Context, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return LoginDirectoryHTTPError{Status: response.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("invalid Login directory response: %w", err)
	}
	return nil
}

func (c *LoginDirectoryClient) ListUsers(ctx context.Context, cursor, query string, limit int) (LoginDirectoryPage, error) {
	values := url.Values{"cursor": {cursor}, "q": {query}, "limit": {strconv.Itoa(limit)}}
	var page LoginDirectoryPage
	err := c.get(ctx, "/api/internal/users?"+values.Encode(), &page)
	return page, err
}

func (c *LoginDirectoryClient) User(ctx context.Context, id string) (LoginDirectoryUser, error) {
	var user LoginDirectoryUser
	err := c.get(ctx, "/api/internal/users/"+url.PathEscape(id), &user)
	return user, err
}
