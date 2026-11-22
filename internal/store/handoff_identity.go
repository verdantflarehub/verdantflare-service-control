package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// HandoffIdentity is a projection of one exact Login subject in one exact
// Center organization. It does not grant Station or project access.
type HandoffIdentity struct {
	SubjectRef       string    `json:"subject_ref"`
	CenterUserRef    string    `json:"center_user_ref"`
	ExternalOrgRef   string    `json:"external_organization_ref"`
	IdentityRevision string    `json:"identity_revision"`
	CheckedAt        time.Time `json:"checked_at"`
}

func (m *Memory) handoffIdentity(subject, orgID, expectedCenterUserID string) (HandoffIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handoffIdentityLocked(subject, orgID, expectedCenterUserID)
}

func (m *Memory) handoffIdentityLocked(subject, orgID, expectedCenterUserID string) (HandoffIdentity, error) {
	if m.identityRevision == 0 {
		return HandoffIdentity{}, identityUnavailable()
	}
	user, found := m.users[subject]
	if !found || user.LoginSubject != subject || user.CenterUserID == "" || orgID == "" {
		return HandoffIdentity{}, identityDenied()
	}
	if expectedCenterUserID != "" && user.CenterUserID != expectedCenterUserID {
		return HandoffIdentity{}, identityDenied()
	}
	organization, found := m.organizations[orgID]
	if !found || organization.Status != "正常" {
		return HandoffIdentity{}, identityDenied()
	}
	// Aliases for a single Center user must not disagree on membership facts.
	var canonical []byte
	for _, candidate := range m.users {
		if candidate.CenterUserID != user.CenterUserID {
			continue
		}
		members, valid := normalizedMemberships(candidate)
		if !valid {
			return HandoffIdentity{}, identityDenied()
		}
		raw, err := json.Marshal(members)
		if err != nil {
			return HandoffIdentity{}, identityUnavailable()
		}
		if canonical != nil && string(canonical) != string(raw) {
			return HandoffIdentity{}, identityDenied()
		}
		canonical = raw
	}
	if canonical == nil {
		return HandoffIdentity{}, identityDenied()
	}
	members, valid := normalizedMemberships(user)
	if !valid {
		return HandoffIdentity{}, identityDenied()
	}
	for _, member := range members {
		if member.OrganizationID != orgID {
			continue
		}
		if member.Status != "正常" || len(member.Roles) == 0 {
			return HandoffIdentity{}, identityDenied()
		}
		for _, role := range member.Roles {
			switch role {
			case "organization_admin", "developer", "billing_viewer", "member", "app_ops_admin", "customer_success_admin", "api_ops_admin", "skill_ops_admin":
			default:
				return HandoffIdentity{}, identityDenied()
			}
		}
		return HandoffIdentity{SubjectRef: subject, CenterUserRef: user.CenterUserID, ExternalOrgRef: orgID,
			IdentityRevision: strconv.FormatUint(m.identityRevision, 10), CheckedAt: time.Now().UTC()}, nil
	}
	return HandoffIdentity{}, identityDenied()
}

func (m *Memory) ResolveHandoffIdentity(_ context.Context, subject, orgID string) (HandoffIdentity, error) {
	return m.handoffIdentity(subject, orgID, "")
}

func (m *Memory) CheckHandoffIdentity(_ context.Context, subject, centerUserID, orgID string) (HandoffIdentity, error) {
	return m.handoffIdentity(subject, orgID, centerUserID)
}

func (p *Postgres) ResolveHandoffIdentity(ctx context.Context, subject, orgID string) (HandoffIdentity, error) {
	snapshot, err := p.read(ctx)
	if err != nil {
		return HandoffIdentity{}, identityUnavailable()
	}
	return snapshot.handoffIdentity(subject, orgID, "")
}

func (p *Postgres) CheckHandoffIdentity(ctx context.Context, subject, centerUserID, orgID string) (HandoffIdentity, error) {
	snapshot, err := p.read(ctx)
	if err != nil {
		return HandoffIdentity{}, identityUnavailable()
	}
	return snapshot.handoffIdentity(subject, orgID, centerUserID)
}
