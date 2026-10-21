package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const mcpTokenPrefix = "vfhub_"

type CreateMCPTokenInput struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expiresInDays,omitempty"`
}

type CreateMCPTokenResult struct {
	domain.MCPToken
	Secret string `json:"secret"`
}

// MCP tokens reuse the Hub operations role: whoever may publish from the Hub
// operations page may issue a token that publishes on their behalf.
func (s *Service) ListMCPTokens(ctx context.Context, subject string) ([]domain.MCPToken, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListMCPTokens(ctx, subject)
}

func (s *Service) CreateMCPToken(ctx context.Context, subject string, input CreateMCPTokenInput) (CreateMCPTokenResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return CreateMCPTokenResult{}, err
	}
	name := strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 50 || input.ExpiresInDays < 0 || input.ExpiresInDays > 365 {
		return CreateMCPTokenResult{}, domain.NewError(400, "mcp_token_invalid", "名称须为 2–50 个字符，有效期为 0–365 天（0 表示不过期）")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return CreateMCPTokenResult{}, domain.NewError(503, "mcp_token_unavailable", "暂时无法生成访问令牌")
	}
	secret := mcpTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	token := domain.MCPToken{ID: newID("mcp"), LoginSubject: subject, Name: name,
		Prefix: secret[:10] + "••••" + secret[len(secret)-4:], CreatedAt: now, SecretHash: sha256.Sum256([]byte(secret))}
	if input.ExpiresInDays > 0 {
		expires := now.AddDate(0, 0, input.ExpiresInDays)
		token.ExpiresAt = &expires
	}
	created, err := s.repository.CreateMCPToken(ctx, token)
	if err != nil {
		return CreateMCPTokenResult{}, err
	}
	return CreateMCPTokenResult{MCPToken: created, Secret: secret}, nil
}

func (s *Service) RevokeMCPToken(ctx context.Context, subject, tokenID string) error {
	if _, _, _, err := s.activeOrganization(ctx, subject); err != nil {
		return err
	}
	return s.repository.RevokeMCPToken(ctx, subject, tokenID, time.Now().UTC())
}

// AuthenticateMCPToken resolves a bearer secret to its Login subject. Role
// checks still run on every business call, so a revoked role disables the
// token immediately.
func (s *Service) AuthenticateMCPToken(ctx context.Context, secret string) (string, error) {
	invalid := domain.NewError(401, "mcp_token_invalid", "MCP 访问令牌无效、已过期或已吊销")
	if !strings.HasPrefix(secret, mcpTokenPrefix) || len(secret) > 128 {
		return "", invalid
	}
	token, err := s.repository.MCPTokenBySecretHash(ctx, sha256.Sum256([]byte(secret)))
	if err != nil || token.Status != "有效" {
		return "", invalid
	}
	if err := s.requireOperationsRole(ctx, token.LoginSubject, "app_ops_admin"); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if token.LastUsedAt == nil || now.Sub(*token.LastUsedAt) > time.Hour {
		_ = s.repository.TouchMCPToken(ctx, token.ID, now)
	}
	return token.LoginSubject, nil
}
