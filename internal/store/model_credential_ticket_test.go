package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestModelCredentialTicketBoundToTokenAndSingleUse(t *testing.T) {
	repository := NewMemoryBootstrap()
	ctx := context.Background()
	now := time.Now().UTC()
	ticket := domain.ModelCredentialTicket{HandleHash: "opaque-digest", TokenID: "model-token", LoginSubject: "operator",
		ModelID: "internal-model", ExpiresAt: now.Add(time.Minute), Status: "prepared"}
	require.NoError(t, repository.CreateModelCredentialTicket(ctx, ticket))
	_, err := repository.ConsumeModelCredentialTicket(ctx, "opaque-digest", "other-token", "operator", now)
	require.Error(t, err)
	result, err := repository.ConsumeModelCredentialTicket(ctx, "opaque-digest", "model-token", "operator", now)
	require.NoError(t, err)
	assert.Equal(t, "processing", result.Status)
	_, err = repository.ConsumeModelCredentialTicket(ctx, "opaque-digest", "model-token", "operator", now)
	require.Error(t, err)
	require.NoError(t, repository.FinishModelCredentialTicket(ctx, "opaque-digest", "applied", 42))
	result, err = repository.ModelCredentialTicket(ctx, "opaque-digest", "model-token", "operator")
	require.NoError(t, err)
	assert.Equal(t, "applied", result.Status)
	assert.Equal(t, 42, result.ChannelID)
}

func TestModelCredentialTicketExpiresWithoutConsumption(t *testing.T) {
	repository := NewMemoryBootstrap()
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, repository.CreateModelCredentialTicket(ctx, domain.ModelCredentialTicket{
		HandleHash: "expired", TokenID: "model-token", LoginSubject: "operator", Status: "prepared", ExpiresAt: now.Add(-time.Second),
	}))
	_, err := repository.ConsumeModelCredentialTicket(ctx, "expired", "model-token", "operator", now)
	require.Error(t, err)
}
