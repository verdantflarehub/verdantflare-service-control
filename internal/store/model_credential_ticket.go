package store

import (
	"context"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) CreateModelCredentialTicket(_ context.Context, ticket domain.ModelCredentialTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.credentialTickets == nil {
		m.credentialTickets = map[string]domain.ModelCredentialTicket{}
	}
	for key, prior := range m.credentialTickets {
		if time.Since(prior.ExpiresAt) > 24*time.Hour {
			delete(m.credentialTickets, key)
		}
	}
	if _, exists := m.credentialTickets[ticket.HandleHash]; exists {
		return domain.NewError(409, "MODEL_CREDENTIAL_HANDLE_CONFLICT", "凭据句柄已存在")
	}
	m.credentialTickets[ticket.HandleHash] = ticket
	return nil
}

func (m *Memory) ModelCredentialTicket(_ context.Context, digest, tokenID, subject string) (domain.ModelCredentialTicket, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ticket, exists := m.credentialTickets[digest]
	if !exists || ticket.TokenID != tokenID || ticket.LoginSubject != subject {
		return domain.ModelCredentialTicket{}, domain.NewError(404, "MODEL_CREDENTIAL_HANDLE_NOT_FOUND", "凭据句柄不存在")
	}
	return ticket, nil
}

func (m *Memory) ConsumeModelCredentialTicket(_ context.Context, digest, tokenID, subject string, at time.Time) (domain.ModelCredentialTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, exists := m.credentialTickets[digest]
	if !exists || ticket.TokenID != tokenID || ticket.LoginSubject != subject {
		return domain.ModelCredentialTicket{}, domain.NewError(404, "MODEL_CREDENTIAL_HANDLE_NOT_FOUND", "凭据句柄不存在")
	}
	if ticket.Status != "prepared" || !at.Before(ticket.ExpiresAt) {
		return domain.ModelCredentialTicket{}, domain.NewError(409, "MODEL_CREDENTIAL_HANDLE_USED", "凭据句柄已使用或过期")
	}
	ticket.Status = "processing"
	m.credentialTickets[digest] = ticket
	return ticket, nil
}

func (m *Memory) FinishModelCredentialTicket(_ context.Context, digest, status string, channelID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, exists := m.credentialTickets[digest]
	if !exists || ticket.Status != "processing" || (status != "applied" && status != "conflict" && status != "unknown") {
		return domain.NewError(409, "MODEL_CREDENTIAL_HANDLE_CHANGED", "凭据句柄状态已变化")
	}
	ticket.Status = status
	ticket.ChannelID = channelID
	m.credentialTickets[digest] = ticket
	return nil
}
