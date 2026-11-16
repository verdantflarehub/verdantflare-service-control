package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// AssistantIdentity is an internal projection, never a replacement for Hub's
// existing context API. Revisions are strings to preserve integers in JS clients.
type AssistantIdentity struct {
	UserID     string    `json:"center_user_id"`
	OrgID      string    `json:"active_org_id"`
	OrgStatus  string    `json:"org_status"`
	MemberRole string    `json:"member_role"`
	Active     bool      `json:"active"`
	CheckedAt  time.Time `json:"checked_at"`
	Revision   string    `json:"identity_revision"`
}

func identityDenied() error {
	return domain.NewError(403, "IDENTITY_DENIED", "身份或组织不可用")
}
func identityUnavailable() error {
	return domain.NewError(503, "IDENTITY_UNAVAILABLE", "身份状态不可验证")
}

func normalizedMemberships(user domain.CenterUser) ([]domain.Membership, bool) {
	result := make([]domain.Membership, 0, len(user.Memberships))
	seen := map[string]bool{}
	for _, member := range user.Memberships {
		if member.OrganizationID == "" || seen[member.OrganizationID] {
			return nil, false
		}
		seen[member.OrganizationID] = true
		member.Roles = append([]string(nil), member.Roles...)
		sort.Strings(member.Roles)
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OrganizationID < result[j].OrganizationID })
	return result, true
}

func (m *Memory) assistantIdentity(subject, userID, orgID string) (AssistantIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.identityRevision == 0 {
		return AssistantIdentity{}, identityUnavailable()
	}
	if subject != "" {
		user, found := m.users[subject]
		if !found {
			return AssistantIdentity{}, identityDenied()
		}
		userID, orgID = user.CenterUserID, user.ActiveOrganizationID
	}
	if userID == "" || orgID == "" {
		return AssistantIdentity{}, identityDenied()
	}
	var canonical []domain.Membership
	var canonicalJSON []byte
	for _, user := range m.users {
		if user.CenterUserID != userID {
			continue
		}
		members, valid := normalizedMemberships(user)
		if !valid {
			return AssistantIdentity{}, identityDenied()
		}
		raw, err := json.Marshal(members)
		if err != nil {
			return AssistantIdentity{}, identityUnavailable()
		}
		if canonicalJSON != nil && string(raw) != string(canonicalJSON) {
			return AssistantIdentity{}, identityDenied()
		}
		canonical, canonicalJSON = members, raw
	}
	if canonicalJSON == nil {
		return AssistantIdentity{}, identityDenied()
	}
	organization, found := m.organizations[orgID]
	if !found || organization.Status != "正常" {
		return AssistantIdentity{}, identityDenied()
	}
	for _, member := range canonical {
		if member.OrganizationID != orgID {
			continue
		}
		if member.Status != "正常" || len(member.Roles) == 0 {
			return AssistantIdentity{}, identityDenied()
		}
		role := "member"
		for _, candidate := range member.Roles {
			switch candidate {
			case "organization_admin":
				role = candidate
			case "developer", "billing_viewer":
				if role != "organization_admin" {
					role = candidate
				}
			case "member", "app_ops_admin", "customer_success_admin", "api_ops_admin":
			default:
				return AssistantIdentity{}, identityDenied()
			}
		}
		return AssistantIdentity{UserID: userID, OrgID: orgID, OrgStatus: "active", MemberRole: role,
			Active: true, CheckedAt: time.Now().UTC(), Revision: strconv.FormatUint(m.identityRevision, 10)}, nil
	}
	return AssistantIdentity{}, identityDenied()
}

func (p *Postgres) ResolveAssistantIdentity(ctx context.Context, subject string) (AssistantIdentity, error) {
	if subject == "" {
		return AssistantIdentity{}, identityDenied()
	}
	snapshot, err := p.read(ctx)
	if err != nil {
		return AssistantIdentity{}, identityUnavailable()
	}
	return snapshot.assistantIdentity(subject, "", "")
}

func (p *Postgres) CheckAssistantIdentity(ctx context.Context, userID, orgID string) (AssistantIdentity, error) {
	snapshot, err := p.read(ctx)
	if err != nil {
		return AssistantIdentity{}, identityUnavailable()
	}
	return snapshot.assistantIdentity("", userID, orgID)
}

// Caller must keep the assistant listener disabled during restore. floor must be
// at least the externally retained pre-restore high-water mark; not a backup value.
func (p *Postgres) AdvanceAssistantIdentityRevision(ctx context.Context, floor uint64) error {
	return p.mutate(ctx, func(m *Memory) error {
		if m.identityRevision > floor {
			floor = m.identityRevision
		}
		if floor == math.MaxUint64 {
			return identityUnavailable()
		}
		m.identityRevision = floor + 1
		return nil
	})
}

func (p *Postgres) AssistantIdentityReady(ctx context.Context) error {
	snapshot, err := p.read(ctx)
	if err != nil || snapshot.identityRevision == 0 {
		return identityUnavailable()
	}
	return nil
}

func authorizationFingerprint(m *Memory) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Users         map[string]domain.CenterUser
		Organizations map[string]domain.Organization
	}{m.users, m.organizations})
	return sha256.Sum256(raw), err
}
