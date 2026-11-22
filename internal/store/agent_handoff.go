package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// HandoffIntent is private repository state. Never return this entire object
// from a browser or public catalog handler: it contains nonce and state.
type HandoffIntent struct {
	RequestRef        string        `json:"requestRef"`
	RequestHash       string        `json:"requestHash"`
	ProviderRef       string        `json:"providerRef"`
	IntentID          string        `json:"intentId"`
	RecipientRef      string        `json:"recipientRef"`
	StationID         string        `json:"stationId"`
	LocalOrgRef       string        `json:"localOrgRef"`
	ExternalOrgRef    string        `json:"externalOrgRef"`
	CallbackRef       string        `json:"callbackRef"`
	CallbackURL       string        `json:"callbackUrl"`
	BrowserOrigin     string        `json:"browserOrigin"`
	ConfigRevision    string        `json:"configRevision"`
	Nonce             string        `json:"nonce"`
	CreatedAt         time.Time     `json:"createdAt"`
	ExpiresAt         time.Time     `json:"expiresAt"`
	Status            string        `json:"status"`
	BrowserState      string        `json:"browserState,omitempty"`
	HandoffRef        string        `json:"handoffRef,omitempty"`
	SubjectRef        string        `json:"subjectRef,omitempty"`
	CenterUserRef     string        `json:"centerUserRef,omitempty"`
	AuthenticationRef string        `json:"authenticationRef,omitempty"`
	LoginSessionRef   string        `json:"loginSessionRef,omitempty"`
	LoginExpiresAt    time.Time     `json:"loginExpiresAt,omitempty"`
	ExchangeID        string        `json:"exchangeId,omitempty"`
	ExchangeHash      string        `json:"exchangeHash,omitempty"`
	Proof             *HandoffProof `json:"proof,omitempty"`
}

type HandoffProof struct {
	ProtocolVersion     int       `json:"protocol_version"`
	Issuer              string    `json:"issuer"`
	EventID             string    `json:"event_id"`
	IntentID            string    `json:"intent_id"`
	ProviderRef         string    `json:"provider_ref"`
	RecipientRef        string    `json:"recipient_client_ref"`
	StationID           string    `json:"station_id"`
	LocalOrgRef         string    `json:"target_local_organization_ref"`
	SubjectRef          string    `json:"subject_ref"`
	CenterUserRef       string    `json:"center_user_ref"`
	ExternalOrgRef      string    `json:"external_organization_ref"`
	Nonce               string    `json:"nonce"`
	Active              bool      `json:"active"`
	IssuedAt            time.Time `json:"issued_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	LoginExpiresAt      time.Time `json:"login_expires_at"`
	CheckedAt           time.Time `json:"checked_at"`
	IdentityRevision    string    `json:"identity_revision"`
	AuthenticationRef   string    `json:"authentication_ref"`
	AuthenticationEpoch string    `json:"authentication_epoch"`
}

type HandoffLoginFact struct {
	SubjectRef        string
	AuthenticationRef string
	ExpiresAt         time.Time
	CheckedAt         time.Time
}

func handoffProblem(status int, code string) error {
	return domain.NewError(status, code, code)
}

func handoffIdentityProblem(err error) error {
	var problem *domain.Error
	if errors.As(err, &problem) && problem.Status == 503 {
		return handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return handoffProblem(403, "HANDOFF_FORBIDDEN")
}

func NewHandoffRef() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (m *Memory) registerHandoffLocked(intent HandoffIntent, now time.Time) (HandoffIntent, error) {
	if m.handoffIntents == nil {
		m.handoffIntents = map[string]HandoffIntent{}
	}
	for ref, existing := range m.handoffIntents {
		// Keep authentication facts for the consumer's longest 8h business
		// session, plus margin. The short handoff window is not the status TTL.
		if now.After(existing.ExpiresAt.Add(13 * time.Hour)) {
			delete(m.handoffIntents, ref)
			continue
		}
		if existing.RecipientRef == intent.RecipientRef && existing.IntentID == intent.IntentID {
			if existing.RequestHash == intent.RequestHash {
				return existing, nil
			}
			return HandoffIntent{}, handoffProblem(409, "HANDOFF_CONFLICT")
		}
		if existing.RecipientRef == intent.RecipientRef && existing.Nonce == intent.Nonce {
			return HandoffIntent{}, handoffProblem(409, "HANDOFF_CONFLICT")
		}
	}
	if len(m.handoffIntents) >= 2000 {
		return HandoffIntent{}, handoffProblem(429, "HANDOFF_RATE_LIMITED")
	}
	if intent.RequestRef == "" || intent.RequestHash == "" || intent.RecipientRef == "" || intent.IntentID == "" || !now.Before(intent.ExpiresAt) {
		return HandoffIntent{}, handoffProblem(400, "HANDOFF_INVALID_REQUEST")
	}
	if _, exists := m.handoffIntents[intent.RequestRef]; exists {
		return HandoffIntent{}, handoffProblem(409, "HANDOFF_CONFLICT")
	}
	intent.Status = "registered"
	m.handoffIntents[intent.RequestRef] = intent
	return intent, nil
}

func (m *Memory) RegisterHandoff(_ context.Context, intent HandoffIntent, now time.Time) (HandoffIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerHandoffLocked(intent, now)
}

func (p *Postgres) RegisterHandoff(ctx context.Context, intent HandoffIntent, now time.Time) (result HandoffIntent, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.registerHandoffLocked(intent, now); return e })
	return
}

func (m *Memory) cancelHandoffLocked(ref, intentID, recipient, station, nonce string) error {
	intent, found := m.handoffIntents[ref]
	if !found {
		return handoffProblem(404, "HANDOFF_NOT_FOUND")
	}
	if intent.IntentID != intentID || intent.RecipientRef != recipient || intent.StationID != station || intent.Nonce != nonce {
		return handoffProblem(403, "HANDOFF_FORBIDDEN")
	}
	if intent.Status == "cancelled" {
		return nil
	}
	intent.Status = "cancelled"
	intent.Proof = nil
	m.handoffIntents[ref] = intent
	return nil
}

func (m *Memory) CancelHandoff(_ context.Context, ref, intentID, recipient, station, nonce string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelHandoffLocked(ref, intentID, recipient, station, nonce)
}

func (p *Postgres) CancelHandoff(ctx context.Context, ref, intentID, recipient, station, nonce string) error {
	return p.mutate(ctx, func(m *Memory) error {
		return m.cancelHandoffLocked(ref, intentID, recipient, station, nonce)
	})
}

func (m *Memory) Handoff(_ context.Context, ref string) (HandoffIntent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	intent, found := m.handoffIntents[ref]
	if !found {
		return HandoffIntent{}, handoffProblem(404, "HANDOFF_NOT_FOUND")
	}
	return intent, nil
}

func (p *Postgres) Handoff(ctx context.Context, ref string) (HandoffIntent, error) {
	m, err := p.read(ctx)
	if err != nil {
		return HandoffIntent{}, handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return m.Handoff(ctx, ref)
}

func (m *Memory) HandoffByAuthentication(_ context.Context, ref string) (HandoffIntent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, intent := range m.handoffIntents {
		if intent.AuthenticationRef == ref && intent.Status == "exchanged" {
			return intent, nil
		}
	}
	return HandoffIntent{}, handoffProblem(404, "HANDOFF_NOT_FOUND")
}

func (p *Postgres) HandoffByAuthentication(ctx context.Context, ref string) (HandoffIntent, error) {
	m, err := p.read(ctx)
	if err != nil {
		return HandoffIntent{}, handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return m.HandoffByAuthentication(ctx, ref)
}

func (m *Memory) confirmHandoffLocked(ref, state, handoffRef, authRef string, fact HandoffLoginFact, now time.Time) (HandoffIntent, error) {
	intent, found := m.handoffIntents[ref]
	if !found {
		return HandoffIntent{}, handoffProblem(404, "HANDOFF_NOT_FOUND")
	}
	if !now.Before(intent.ExpiresAt) {
		return HandoffIntent{}, handoffProblem(410, "HANDOFF_EXPIRED")
	}
	if intent.Status != "registered" {
		return HandoffIntent{}, handoffProblem(409, "HANDOFF_CONFLICT")
	}
	if state == "" || len(state) > 256 || handoffRef == "" || authRef == "" || fact.SubjectRef == "" || fact.AuthenticationRef == "" || !now.Before(fact.ExpiresAt) {
		return HandoffIntent{}, handoffProblem(400, "HANDOFF_INVALID_REQUEST")
	}
	identity, err := m.handoffIdentityLocked(fact.SubjectRef, intent.ExternalOrgRef, "")
	if err != nil {
		return HandoffIntent{}, handoffIdentityProblem(err)
	}
	intent.Status = "confirmed"
	intent.BrowserState = state
	intent.HandoffRef = handoffRef
	intent.SubjectRef = fact.SubjectRef
	intent.CenterUserRef = identity.CenterUserRef
	intent.AuthenticationRef = authRef
	intent.LoginSessionRef = fact.AuthenticationRef
	intent.LoginExpiresAt = fact.ExpiresAt
	m.handoffIntents[ref] = intent
	return intent, nil
}

func (m *Memory) ConfirmHandoff(_ context.Context, ref, state, handoffRef, authRef string, fact HandoffLoginFact, now time.Time) (HandoffIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.confirmHandoffLocked(ref, state, handoffRef, authRef, fact, now)
}

func (p *Postgres) ConfirmHandoff(ctx context.Context, ref, state, handoffRef, authRef string, fact HandoffLoginFact, now time.Time) (result HandoffIntent, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.confirmHandoffLocked(ref, state, handoffRef, authRef, fact, now)
		return e
	})
	return
}

type HandoffExchangeInput struct {
	ProviderRef  string `json:"provider_ref"`
	IntentID     string `json:"intent_id"`
	RequestRef   string `json:"request_ref"`
	HandoffRef   string `json:"handoff_ref"`
	RecipientRef string `json:"recipient_client_ref"`
	StationID    string `json:"station_id"`
	LocalOrgRef  string `json:"target_local_organization_ref"`
	Nonce        string `json:"nonce"`
	ExchangeID   string `json:"exchange_id"`
}

func (m *Memory) exchangeHandoffLocked(input HandoffExchangeInput, hash, eventID, issuer string, fact HandoffLoginFact, now time.Time) (HandoffProof, error) {
	intent, found := m.handoffIntents[input.RequestRef]
	if !found {
		return HandoffProof{}, handoffProblem(404, "HANDOFF_NOT_FOUND")
	}
	if !now.Before(intent.ExpiresAt) {
		return HandoffProof{}, handoffProblem(410, "HANDOFF_EXPIRED")
	}
	if intent.ProviderRef != input.ProviderRef || intent.IntentID != input.IntentID || intent.HandoffRef != input.HandoffRef || intent.RecipientRef != input.RecipientRef || intent.StationID != input.StationID || intent.LocalOrgRef != input.LocalOrgRef || intent.Nonce != input.Nonce || input.ExchangeID == "" {
		return HandoffProof{}, handoffProblem(403, "HANDOFF_FORBIDDEN")
	}
	if intent.Status == "exchanged" {
		if intent.ExchangeID == input.ExchangeID && intent.ExchangeHash == hash && intent.Proof != nil && now.Before(intent.Proof.ExpiresAt) {
			if fact.SubjectRef != intent.SubjectRef || fact.AuthenticationRef != intent.LoginSessionRef || !now.Before(fact.ExpiresAt) || intent.Proof.AuthenticationEpoch != strconv.FormatUint(m.handoffEpoch, 10) {
				return HandoffProof{}, handoffProblem(403, "HANDOFF_REVOKED")
			}
			identity, err := m.handoffIdentityLocked(intent.SubjectRef, intent.ExternalOrgRef, intent.CenterUserRef)
			if err != nil || identity.IdentityRevision != intent.Proof.IdentityRevision {
				return HandoffProof{}, handoffProblem(403, "HANDOFF_REVOKED")
			}
			return *intent.Proof, nil
		}
		return HandoffProof{}, handoffProblem(409, "HANDOFF_CONFLICT")
	}
	if intent.Status != "confirmed" {
		return HandoffProof{}, handoffProblem(409, "HANDOFF_CONFLICT")
	}
	if fact.SubjectRef != intent.SubjectRef || fact.AuthenticationRef != intent.LoginSessionRef || !now.Before(fact.ExpiresAt) {
		return HandoffProof{}, handoffProblem(403, "HANDOFF_REVOKED")
	}
	identity, err := m.handoffIdentityLocked(intent.SubjectRef, intent.ExternalOrgRef, intent.CenterUserRef)
	if err != nil {
		return HandoffProof{}, handoffIdentityProblem(err)
	}
	expires := now.Add(time.Minute)
	if intent.ExpiresAt.Before(expires) {
		expires = intent.ExpiresAt
	}
	if fact.ExpiresAt.Before(expires) {
		expires = fact.ExpiresAt
	}
	if !now.Before(expires) {
		return HandoffProof{}, handoffProblem(410, "HANDOFF_EXPIRED")
	}
	if m.handoffEpoch == 0 {
		m.handoffEpoch = 1
	}
	proof := HandoffProof{ProtocolVersion: 1, Issuer: issuer, EventID: eventID, IntentID: intent.IntentID,
		ProviderRef: intent.ProviderRef, RecipientRef: intent.RecipientRef, StationID: intent.StationID,
		LocalOrgRef: intent.LocalOrgRef, SubjectRef: intent.SubjectRef, CenterUserRef: intent.CenterUserRef,
		ExternalOrgRef: intent.ExternalOrgRef, Nonce: intent.Nonce, Active: true, IssuedAt: now,
		ExpiresAt: expires, LoginExpiresAt: fact.ExpiresAt, CheckedAt: fact.CheckedAt,
		IdentityRevision: identity.IdentityRevision, AuthenticationRef: intent.AuthenticationRef,
		AuthenticationEpoch: strconv.FormatUint(m.handoffEpoch, 10)}
	intent.Status = "exchanged"
	intent.ExchangeID = input.ExchangeID
	intent.ExchangeHash = hash
	intent.Proof = &proof
	m.handoffIntents[input.RequestRef] = intent
	return proof, nil
}

func (m *Memory) ExchangeHandoff(_ context.Context, input HandoffExchangeInput, hash, eventID, issuer string, fact HandoffLoginFact, now time.Time) (HandoffProof, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exchangeHandoffLocked(input, hash, eventID, issuer, fact, now)
}

func (p *Postgres) ExchangeHandoff(ctx context.Context, input HandoffExchangeInput, hash, eventID, issuer string, fact HandoffLoginFact, now time.Time) (result HandoffProof, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.exchangeHandoffLocked(input, hash, eventID, issuer, fact, now)
		return e
	})
	return
}

func (m *Memory) CheckHandoff(_ context.Context, ref, epoch, subject, centerUserID, orgID, recipient, stationID string) (HandoffIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, intent := range m.handoffIntents {
		if intent.AuthenticationRef != ref || intent.SubjectRef != subject || intent.CenterUserRef != centerUserID || intent.ExternalOrgRef != orgID || intent.RecipientRef != recipient || intent.StationID != stationID || intent.Status != "exchanged" || intent.Proof == nil {
			continue
		}
		if intent.Proof.AuthenticationEpoch == epoch && epoch == strconv.FormatUint(m.handoffEpoch, 10) {
			identity, err := m.handoffIdentityLocked(subject, orgID, centerUserID)
			if err != nil {
				return HandoffIdentity{}, handoffIdentityProblem(err)
			}
			return identity, nil
		}
	}
	return HandoffIdentity{}, handoffProblem(403, "HANDOFF_REVOKED")
}

func (p *Postgres) CheckHandoff(ctx context.Context, ref, epoch, subject, centerUserID, orgID, recipient, stationID string) (HandoffIdentity, error) {
	m, err := p.read(ctx)
	if err != nil {
		return HandoffIdentity{}, handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return m.CheckHandoff(ctx, ref, epoch, subject, centerUserID, orgID, recipient, stationID)
}

func (m *Memory) RevokeAllHandoffs(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handoffEpoch == ^uint64(0) {
		return errors.New("handoff epoch exhausted")
	}
	m.handoffEpoch++
	return nil
}

func (p *Postgres) RevokeAllHandoffs(ctx context.Context) error {
	return p.mutate(ctx, func(m *Memory) error {
		if m.handoffEpoch == ^uint64(0) {
			return handoffProblem(503, "HANDOFF_UNAVAILABLE")
		}
		m.handoffEpoch++
		return nil
	})
}

// AdvanceAgentHandoffEpoch is an explicit operations action before enabling
// the listener or after restoring a backup. floor must come from an external
// retained high-water mark, never from the restored snapshot alone.
func (p *Postgres) AdvanceAgentHandoffEpoch(ctx context.Context, floor uint64) error {
	return p.mutate(ctx, func(m *Memory) error {
		if m.handoffEpoch > floor {
			floor = m.handoffEpoch
		}
		if floor == math.MaxUint64 {
			return handoffProblem(503, "HANDOFF_UNAVAILABLE")
		}
		m.handoffEpoch = floor + 1
		return nil
	})
}

func (p *Postgres) AgentHandoffReady(ctx context.Context, identityFloor, epochFloor uint64) error {
	m, err := p.read(ctx)
	if err != nil || identityFloor == 0 || epochFloor == 0 || m.identityRevision < identityFloor || m.handoffEpoch < epochFloor {
		return handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return nil
}

func (p *Postgres) AgentHandoffHighWater(ctx context.Context) (identityRevision, authenticationEpoch uint64, err error) {
	m, err := p.read(ctx)
	if err != nil {
		return 0, 0, handoffProblem(503, "HANDOFF_UNAVAILABLE")
	}
	return m.identityRevision, m.handoffEpoch, nil
}
