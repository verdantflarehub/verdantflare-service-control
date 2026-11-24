package store

import (
	"context"
	"strconv"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func directoryRevisionChanged() error {
	return domain.NewError(409, "HANDOFF_REVISION_CHANGED", "身份目录已更新")
}

// DirectoryRevision is a non-profile version probe for a registered consumer.
func (m *Memory) DirectoryRevision(_ context.Context) (string, time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.identityRevision == 0 {
		return "", time.Time{}, identityUnavailable()
	}
	return strconv.FormatUint(m.identityRevision, 10), time.Now().UTC(), nil
}

func (p *Postgres) DirectoryRevision(ctx context.Context) (string, time.Time, error) {
	snapshot, err := p.read(ctx)
	if err != nil {
		return "", time.Time{}, identityUnavailable()
	}
	return snapshot.DirectoryRevision(ctx)
}

// CheckDirectoryMember must be followed by a fresh Login check and then a
// second call at the same expected revision before the provider returns success.
func (m *Memory) CheckDirectoryMember(_ context.Context, subject, orgID, expectedRevision string) (HandoffIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.identityRevision == 0 {
		return HandoffIdentity{}, identityUnavailable()
	}
	current := strconv.FormatUint(m.identityRevision, 10)
	if current != expectedRevision {
		return HandoffIdentity{}, directoryRevisionChanged()
	}
	fact, err := m.handoffIdentityLocked(subject, orgID, "")
	if err != nil {
		return HandoffIdentity{}, err
	}
	user := m.users[subject]
	members, valid := normalizedMemberships(user)
	if !valid {
		return HandoffIdentity{}, identityDenied()
	}
	for _, member := range members {
		if member.OrganizationID != orgID || member.Status != "正常" {
			continue
		}
		for _, role := range member.Roles {
			if role == "member" {
				return fact, nil
			}
		}
	}
	return HandoffIdentity{}, identityDenied()
}

func (p *Postgres) CheckDirectoryMember(ctx context.Context, subject, orgID, expectedRevision string) (HandoffIdentity, error) {
	snapshot, err := p.read(ctx)
	if err != nil {
		return HandoffIdentity{}, identityUnavailable()
	}
	return snapshot.CheckDirectoryMember(ctx, subject, orgID, expectedRevision)
}
