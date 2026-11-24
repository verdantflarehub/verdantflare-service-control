package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type LoginHandoffFact struct {
	SubjectRef        string    `json:"subject_ref"`
	AuthenticationRef string    `json:"authentication_ref"`
	LoginExpiresAt    time.Time `json:"login_expires_at"`
	CheckedAt         time.Time `json:"checked_at"`
}

type LoginSubjectFact struct {
	SubjectRef string    `json:"subject_ref"`
	Active     bool      `json:"active"`
	CheckedAt  time.Time `json:"checked_at"`
}

type LoginHandoffClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewLoginHandoffClient(baseURL, token string) (*LoginHandoffClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || len(token) < 32 {
		return nil, errors.New("invalid Login handoff client configuration")
	}
	return &LoginHandoffClient{baseURL: strings.TrimRight(baseURL, "/"), token: token,
		client: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *LoginHandoffClient) Resolve(ctx context.Context, cookie string) (LoginHandoffFact, error) {
	if cookie == "" || len(cookie) > 8192 {
		return LoginHandoffFact{}, domain.NewError(401, "HANDOFF_UNAUTHENTICATED", "登录会话不可用")
	}
	return c.call(ctx, "/api/internal/agent-handoff/v1/session/resolve", nil, cookie)
}

func (c *LoginHandoffClient) Check(ctx context.Context, ref, subject string) (LoginHandoffFact, error) {
	if ref == "" || subject == "" {
		return LoginHandoffFact{}, domain.NewError(400, "HANDOFF_INVALID_REQUEST", "认证引用不完整")
	}
	body, _ := json.Marshal(map[string]string{"authentication_ref": ref, "subject_ref": subject})
	return c.call(ctx, "/api/internal/agent-handoff/v1/session/check", body, "")
}

// CheckSubject is deliberately session-free for pre-business-session member
// directory verification. The 700ms deadline leaves room in Core's 1s budget.
func (c *LoginHandoffClient) CheckSubject(ctx context.Context, subject string) (LoginSubjectFact, error) {
	if subject == "" || len(subject) > 256 {
		return LoginSubjectFact{}, domain.NewError(400, "HANDOFF_INVALID_REQUEST", "主体引用无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"subject_ref": subject})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/internal/agent-handoff/v1/subjects/check", bytes.NewReader(body))
	if err != nil {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return LoginSubjectFact{}, domain.NewError(403, "HANDOFF_FORBIDDEN", "主体不可用")
	}
	if response.StatusCode != http.StatusOK {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	fact, err := decodeLoginSubjectFact(raw)
	if err != nil {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	now := time.Now().UTC()
	if fact.SubjectRef != subject || !fact.Active || fact.CheckedAt.Before(now.Add(-5*time.Second)) || fact.CheckedAt.After(now.Add(5*time.Second)) {
		return LoginSubjectFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	return fact, nil
}

func decodeLoginSubjectFact(raw []byte) (LoginSubjectFact, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return LoginSubjectFact{}, errors.New("object required")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return LoginSubjectFact{}, err
		}
		key, ok := keyToken.(string)
		if !ok || fields[key] != nil || (key != "subject_ref" && key != "active" && key != "checked_at") {
			return LoginSubjectFact{}, errors.New("unexpected Login fact field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return LoginSubjectFact{}, err
		}
		fields[key] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(fields) != 3 {
		return LoginSubjectFact{}, errors.New("incomplete Login fact")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return LoginSubjectFact{}, errors.New("trailing Login fact")
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return LoginSubjectFact{}, err
	}
	var fact LoginSubjectFact
	if err := json.Unmarshal(canonical, &fact); err != nil {
		return LoginSubjectFact{}, err
	}
	return fact, nil
}

func (c *LoginHandoffClient) call(ctx context.Context, path string, body []byte, cookie string) (LoginHandoffFact, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return LoginHandoffFact{}, domain.NewError(403, "HANDOFF_REVOKED", "登录状态已失效")
	}
	if response.StatusCode == http.StatusUnauthorized && cookie != "" {
		return LoginHandoffFact{}, domain.NewError(401, "HANDOFF_UNAUTHENTICATED", "登录会话不可用")
	}
	if response.StatusCode != http.StatusOK {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fact LoginHandoffFact
	if err := decoder.Decode(&fact); err != nil {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || fact.SubjectRef == "" || fact.AuthenticationRef == "" || !time.Now().Before(fact.LoginExpiresAt) {
		return LoginHandoffFact{}, domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login 状态不可验证")
	}
	return fact, nil
}
