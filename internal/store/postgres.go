package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// Postgres persists the initial modular-monolith repository as one JSONB aggregate.
// Mutations lock the single aggregate row, so this implementation is safe across
// multiple processes but intentionally optimized for the first production release.
type Postgres struct {
	db *sql.DB
}

type persistedState struct {
	Users         map[string]domain.CenterUser            `json:"users"`
	Organizations map[string]domain.Organization          `json:"organizations"`
	Apps          map[string]domain.App                   `json:"apps"`
	AppVersions   map[string]map[string]domain.AppVersion `json:"appVersions"`
	StationGrants map[string]domain.StationTestGrant      `json:"stationGrants"`
	Entitlements  map[string]map[string]bool              `json:"entitlements"`
	Sessions      map[string]domain.ExperienceSession     `json:"sessions"`
	ModelRuns     map[string]domain.ModelExperienceRun    `json:"modelRuns"`
	APIKeys       map[string]persistedAPIKey              `json:"apiKeys"`
	MCPTokens     map[string]persistedMCPToken            `json:"mcpTokens,omitempty"`
	Models        []domain.Model                          `json:"models"`
	PublicModels  map[string]domain.PublicModel           `json:"publicModels"`
	ModelIcons    map[string]domain.ModelProviderIcon     `json:"modelIcons,omitempty"`
	Tasks         []domain.APITask                        `json:"tasks"`
	Members       map[string][]domain.Member              `json:"members"`
	Billing       map[string]domain.BillingSummary        `json:"billing"`
	Releases      []domain.Release                        `json:"releases"`
	OpsOrgs       []domain.OperationsOrganization         `json:"operationsOrganizations"`
}

// Keep the verifier in private repository state while domain.APIKey continues
// to omit it from every HTTP JSON response.
type persistedAPIKey struct {
	domain.APIKey
	SecretHash [32]byte `json:"secretHash"`
}

type persistedMCPToken struct {
	domain.MCPToken
	LoginSubject string   `json:"loginSubject"`
	SecretHash   [32]byte `json:"secretHash"`
}

func OpenPostgres(databaseURL string, maxOpen, maxIdle int, _ time.Time) (*Postgres, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	seed, err := encodeMemory(NewMemoryBootstrap())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO control_repository_state (singleton, state, updated_at)
		VALUES (TRUE, $1, NOW())
		ON CONFLICT (singleton) DO NOTHING`, seed); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize control repository: %w", err)
	}
	return &Postgres{db: db}, nil
}

func (p *Postgres) Close() error { return p.db.Close() }

func (p *Postgres) read(ctx context.Context) (*Memory, error) {
	var raw []byte
	if err := p.db.QueryRowContext(ctx, `SELECT state FROM control_repository_state WHERE singleton=TRUE`).Scan(&raw); err != nil {
		return nil, fmt.Errorf("read control repository: %w", err)
	}
	return decodeMemory(raw)
}

func (p *Postgres) mutate(ctx context.Context, operation func(*Memory) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin control repository transaction: %w", err)
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state FROM control_repository_state WHERE singleton=TRUE FOR UPDATE`).Scan(&raw); err != nil {
		return fmt.Errorf("lock control repository: %w", err)
	}
	memory, err := decodeMemory(raw)
	if err != nil {
		return err
	}
	if err := operation(memory); err != nil {
		return err
	}
	updated, err := encodeMemory(memory)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_repository_state SET state=$1,updated_at=NOW() WHERE singleton=TRUE`, updated); err != nil {
		return fmt.Errorf("persist control repository: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit control repository: %w", err)
	}
	return nil
}

func encodeMemory(m *Memory) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make(map[string]persistedAPIKey, len(m.apiKeys))
	for id, key := range m.apiKeys {
		keys[id] = persistedAPIKey{APIKey: key, SecretHash: key.SecretHash}
	}
	tokens := make(map[string]persistedMCPToken, len(m.mcpTokens))
	for id, token := range m.mcpTokens {
		tokens[id] = persistedMCPToken{MCPToken: token, LoginSubject: token.LoginSubject, SecretHash: token.SecretHash}
	}
	raw, err := json.Marshal(persistedState{
		Users: m.users, Organizations: m.organizations, Apps: m.apps, AppVersions: m.appVersions, StationGrants: m.stationGrants, Entitlements: m.entitlements,
		Sessions: m.sessions, ModelRuns: m.modelRuns, APIKeys: keys, MCPTokens: tokens, Models: m.models, PublicModels: m.publicModels, ModelIcons: m.modelIcons, Tasks: m.tasks,
		Members: m.members, Billing: m.billing, Releases: m.releases, OpsOrgs: m.opsOrgs,
	})
	if err != nil {
		return nil, fmt.Errorf("encode control repository: %w", err)
	}
	return raw, nil
}

func decodeMemory(raw []byte) (*Memory, error) {
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode control repository: %w", err)
	}
	keys := make(map[string]domain.APIKey, len(state.APIKeys))
	for id, persisted := range state.APIKeys {
		key := persisted.APIKey
		key.SecretHash = persisted.SecretHash
		if key.SecretHash == ([32]byte{}) && key.Status == "有效" {
			key.Status = "需重建"
		}
		keys[id] = key
	}
	tokens := make(map[string]domain.MCPToken, len(state.MCPTokens))
	for id, persisted := range state.MCPTokens {
		token := persisted.MCPToken
		token.LoginSubject, token.SecretHash = persisted.LoginSubject, persisted.SecretHash
		tokens[id] = token
	}
	return &Memory{
		users: state.Users, organizations: state.Organizations, apps: state.Apps, appVersions: state.AppVersions, stationGrants: state.StationGrants,
		entitlements: state.Entitlements, sessions: state.Sessions, modelRuns: state.ModelRuns, apiKeys: keys, mcpTokens: tokens,
		models: state.Models, publicModels: state.PublicModels, modelIcons: state.ModelIcons, tasks: state.Tasks, members: state.Members, billing: state.Billing,
		releases: state.Releases, opsOrgs: state.OpsOrgs,
	}, nil
}

func (p *Postgres) CenterContext(ctx context.Context, subject string) (domain.CenterContext, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.CenterContext{}, err
	}
	return m.CenterContext(ctx, subject)
}
func (p *Postgres) SetActiveOrganization(ctx context.Context, subject, organizationID string) (result domain.CenterContext, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.SetActiveOrganization(ctx, subject, organizationID)
		return e
	})
	return
}
func (p *Postgres) ActiveOrganization(ctx context.Context, subject string) (domain.Organization, []string, string, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.Organization{}, nil, "", err
	}
	return m.ActiveOrganization(ctx, subject)
}
func (p *Postgres) Organization(ctx context.Context, id string) (domain.Organization, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.Organization{}, err
	}
	return m.Organization(ctx, id)
}
func (p *Postgres) UpdateOrganization(ctx context.Context, id string, update domain.Organization) (result domain.Organization, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.UpdateOrganization(ctx, id, update); return e })
	return
}
func (p *Postgres) ListApps(ctx context.Context, organizationID string) ([]domain.App, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListApps(ctx, organizationID)
}
func (p *Postgres) GetApp(ctx context.Context, organizationID, appID string) (domain.App, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.App{}, err
	}
	return m.GetApp(ctx, organizationID, appID)
}
func (p *Postgres) ListExperienceSessions(ctx context.Context, organizationID string) ([]domain.ExperienceSession, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListExperienceSessions(ctx, organizationID)
}
func (p *Postgres) CreateExperienceSession(ctx context.Context, organizationID, centerUserID string, app domain.App, region string, duration time.Duration, reservedCredits int) (result domain.ExperienceSession, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.CreateExperienceSession(ctx, organizationID, centerUserID, app, region, duration, reservedCredits)
		return e
	})
	return
}
func (p *Postgres) CloseExperienceSession(ctx context.Context, organizationID, sessionID string) (result domain.ExperienceSession, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.CloseExperienceSession(ctx, organizationID, sessionID)
		return e
	})
	return
}
func (p *Postgres) ListAPIKeys(ctx context.Context, organizationID string) ([]domain.APIKey, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListAPIKeys(ctx, organizationID)
}
func (p *Postgres) CreateAPIKey(ctx context.Context, organizationID string, key domain.APIKey) (result domain.APIKey, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateAPIKey(ctx, organizationID, key); return e })
	return
}
func (p *Postgres) RevokeAPIKey(ctx context.Context, organizationID, keyID string) error {
	return p.mutate(ctx, func(m *Memory) error { return m.RevokeAPIKey(ctx, organizationID, keyID) })
}
func (p *Postgres) ListModels(ctx context.Context, organizationID string) ([]domain.Model, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListModels(ctx, organizationID)
}
func (p *Postgres) ListAPITasks(ctx context.Context, organizationID string) ([]domain.APITask, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListAPITasks(ctx, organizationID)
}
func (p *Postgres) Usage(ctx context.Context, organizationID string) (domain.UsageSummary, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.UsageSummary{}, err
	}
	return m.Usage(ctx, organizationID)
}
func (p *Postgres) ListMembers(ctx context.Context, organizationID string) ([]domain.Member, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListMembers(ctx, organizationID)
}
func (p *Postgres) BoundLoginSubjects(ctx context.Context) (map[string]bool, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.BoundLoginSubjects(ctx)
}
func (p *Postgres) BindLoginUser(ctx context.Context, organizationID, subject, email, role string) (result domain.Member, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.BindLoginUser(ctx, organizationID, subject, email, role)
		return e
	})
	return
}
func (p *Postgres) TransferLoginUser(ctx context.Context, sourceID, targetID, memberID, role, actorSubject string) (result domain.Member, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.TransferLoginUser(ctx, sourceID, targetID, memberID, role, actorSubject)
		return e
	})
	return
}
func (p *Postgres) AddMember(ctx context.Context, organizationID string, member domain.Member) (result domain.Member, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.AddMember(ctx, organizationID, member); return e })
	return
}
func (p *Postgres) UpdateMember(ctx context.Context, organizationID, memberID, role, status, actorSubject string) (result domain.Member, err error) {
	err = p.mutate(ctx, func(m *Memory) error {
		var e error
		result, e = m.UpdateMember(ctx, organizationID, memberID, role, status, actorSubject)
		return e
	})
	return
}
func (p *Postgres) Billing(ctx context.Context, organizationID string) (domain.BillingSummary, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.BillingSummary{}, err
	}
	return m.Billing(ctx, organizationID)
}
func (p *Postgres) ListReleases(ctx context.Context) ([]domain.Release, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListReleases(ctx)
}
func (p *Postgres) ListOperationsOrganizations(ctx context.Context) ([]domain.OperationsOrganization, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListOperationsOrganizations(ctx)
}

func (p *Postgres) ListMCPTokens(ctx context.Context, subject string) ([]domain.MCPToken, error) {
	m, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	return m.ListMCPTokens(ctx, subject)
}
func (p *Postgres) CreateMCPToken(ctx context.Context, token domain.MCPToken) (result domain.MCPToken, err error) {
	err = p.mutate(ctx, func(m *Memory) error { var e error; result, e = m.CreateMCPToken(ctx, token); return e })
	return
}
func (p *Postgres) RevokeMCPToken(ctx context.Context, subject, tokenID string, at time.Time) error {
	return p.mutate(ctx, func(m *Memory) error { return m.RevokeMCPToken(ctx, subject, tokenID, at) })
}
func (p *Postgres) MCPTokenBySecretHash(ctx context.Context, hash [32]byte) (domain.MCPToken, error) {
	m, err := p.read(ctx)
	if err != nil {
		return domain.MCPToken{}, err
	}
	return m.MCPTokenBySecretHash(ctx, hash)
}
func (p *Postgres) TouchMCPToken(ctx context.Context, tokenID string, at time.Time) error {
	return p.mutate(ctx, func(m *Memory) error { return m.TouchMCPToken(ctx, tokenID, at) })
}
