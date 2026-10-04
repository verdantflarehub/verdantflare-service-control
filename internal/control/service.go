package control

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type Service struct {
	repository     store.Repository
	modelGateway   ModelGateway
	accountGateway GatewayAccounts
	now            func() time.Time
}

type ModelGateway interface {
	ListModels(context.Context) (map[string]struct{}, error)
}

type GatewayAccounts interface {
	Balance(context.Context, string) (gateway.CenterBalance, error)
	Grant(context.Context, string, string, string, int) (gateway.CenterBalance, error)
	SetEnabled(context.Context, string, bool) error
	ListKeys(context.Context, string) ([]gateway.CenterKey, error)
	CreateKey(context.Context, string, string, string, []string, int) (gateway.CenterCreatedKey, error)
	RevokeKey(context.Context, string, int) error
	ProbeKey(context.Context, string, int) (gateway.CenterProbe, error)
}

func (s *Service) SetGatewayAccounts(accounts GatewayAccounts) { s.accountGateway = accounts }

var gatewayRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

func NewService(repository store.Repository, gateways ...ModelGateway) *Service {
	service := &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
	if len(gateways) > 0 {
		service.modelGateway = gateways[0]
	}
	return service
}

type CreateExperienceInput struct {
	AppID  string `json:"appId"`
	Region string `json:"region"`
}

type CreateAPIKeyInput struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expiresInDays"`
	RequestID     string   `json:"requestId"`
}

type CreateAPIKeyResult struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Secret    string     `json:"secret"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type UpdateOrganizationInput struct {
	Name          string `json:"name"`
	ShortName     string `json:"shortName"`
	DefaultRegion string `json:"defaultRegion"`
	Industry      string `json:"industry"`
	BillingEmail  string `json:"billingEmail"`
}

type InviteMemberInput struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Service) Context(ctx context.Context, subject string) (domain.CenterContext, error) {
	return s.repository.CenterContext(ctx, subject)
}

func (s *Service) Overview(ctx context.Context, subject string) (domain.Overview, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Overview{}, err
	}
	apps, err := s.repository.ListApps(ctx, organization.OrganizationID)
	if err != nil {
		return domain.Overview{}, err
	}
	overview := domain.Overview{Organization: organization, AvailableApps: len(apps)}
	for _, app := range apps {
		if app.Channel == "Preview" {
			overview.PreviewApps++
		}
	}
	return overview, nil
}

func (s *Service) SetActiveOrganization(ctx context.Context, subject, organizationID string) (domain.CenterContext, error) {
	if strings.TrimSpace(organizationID) == "" {
		return domain.CenterContext{}, domain.NewError(400, "organization_required", "organizationId 不能为空")
	}
	return s.repository.SetActiveOrganization(ctx, subject, organizationID)
}

func (s *Service) ListApps(ctx context.Context, subject string) ([]domain.App, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	return s.repository.ListApps(ctx, organization.OrganizationID)
}

func (s *Service) GetApp(ctx context.Context, subject, appID string) (domain.App, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.App{}, err
	}
	return s.repository.GetApp(ctx, organization.OrganizationID, appID)
}

func (s *Service) ListExperienceSessions(ctx context.Context, subject string) ([]domain.ExperienceSession, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	return s.repository.ListExperienceSessions(ctx, organization.OrganizationID)
}

func (s *Service) CreateExperienceSession(ctx context.Context, subject string, input CreateExperienceInput) (domain.ExperienceSession, error) {
	if _, _, _, err := s.activeOrganization(ctx, subject); err != nil {
		return domain.ExperienceSession{}, err
	}
	// A database row is not a running workspace. Do not accept trials until a
	// resource allocator, execution path, and cleanup worker are connected.
	return domain.ExperienceSession{}, domain.NewError(503, "experience_unavailable", "在线体验运行资源尚未接入，暂不能创建 Session")
}

func (s *Service) CloseExperienceSession(ctx context.Context, subject, sessionID string) (domain.ExperienceSession, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.ExperienceSession{}, err
	}
	return s.repository.CloseExperienceSession(ctx, organization.OrganizationID, sessionID)
}

func (s *Service) ListAPIKeys(ctx context.Context, subject string) ([]domain.APIKey, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	if s.accountGateway == nil {
		return nil, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	items, err := s.accountGateway.ListKeys(ctx, organization.OrganizationID)
	if err != nil {
		return nil, gatewayAccountError(err)
	}
	keys := make([]domain.APIKey, 0, len(items))
	for _, item := range items {
		key := domain.APIKey{ID: "gw_" + strconv.Itoa(item.ID), OrganizationID: organization.OrganizationID,
			Source: "gateway", Name: item.Name, Prefix: item.Prefix, Scopes: item.Models,
			CreatedAt: time.Unix(item.CreatedAt, 0).UTC(), Status: item.Status}
		if item.ExpiresAt > 0 {
			expires := time.Unix(item.ExpiresAt, 0).UTC()
			key.ExpiresAt = &expires
		}
		if item.LastUsedAt > item.CreatedAt {
			used := time.Unix(item.LastUsedAt, 0).UTC()
			key.LastUsedAt = &used
		}
		keys = append(keys, key)
	}
	legacy, err := s.repository.ListAPIKeys(ctx, organization.OrganizationID)
	if err != nil {
		return nil, err
	}
	for _, key := range legacy {
		key.Source = "legacy"
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, subject string, input CreateAPIKeyInput) (CreateAPIKeyResult, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return CreateAPIKeyResult{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return CreateAPIKeyResult{}, domain.NewError(403, "api_key_forbidden", "当前角色不能创建 API Key")
	}
	if organization.Status == "冻结" {
		return CreateAPIKeyResult{}, domain.NewError(403, "organization_frozen", "组织已冻结")
	}
	if s.accountGateway == nil || s.modelGateway == nil {
		return CreateAPIKeyResult{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	input.Name = strings.TrimSpace(input.Name)
	if len([]rune(input.Name)) < 2 || len([]rune(input.Name)) > 50 || !gatewayRequestIDPattern.MatchString(input.RequestID) || input.ExpiresInDays < 1 || input.ExpiresInDays > 365 || len(input.Scopes) < 1 || len(input.Scopes) > 20 {
		return CreateAPIKeyResult{}, domain.NewError(400, "api_key_invalid", "Key 名称、请求 ID、模型或有效期无效")
	}
	published, err := s.publishedGatewayModels(ctx)
	if err != nil {
		return CreateAPIKeyResult{}, err
	}
	allowed := make(map[string]bool, len(published))
	for _, model := range published {
		allowed[model.ID] = true
	}
	seen := make(map[string]bool)
	for _, id := range input.Scopes {
		if !allowed[id] || seen[id] {
			return CreateAPIKeyResult{}, domain.NewError(400, "api_key_model_forbidden", "Key 只能授权当前已上架且网关可见的模型")
		}
		seen[id] = true
	}
	sort.Strings(input.Scopes)
	created, err := s.accountGateway.CreateKey(ctx, organization.OrganizationID, input.RequestID, input.Name, input.Scopes, input.ExpiresInDays)
	if err != nil {
		return CreateAPIKeyResult{}, gatewayAccountError(err)
	}
	result := CreateAPIKeyResult{ID: "gw_" + strconv.Itoa(created.ID), Name: created.Name,
		Secret: created.Secret, Scopes: created.Models, CreatedAt: time.Unix(created.CreatedAt, 0).UTC()}
	if created.ExpiresAt > 0 {
		expires := time.Unix(created.ExpiresAt, 0).UTC()
		result.ExpiresAt = &expires
	}
	return result, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, subject, keyID string) error {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return err
	}
	if !hasAnyRole(roles, "organization_admin", "api_ops_admin") {
		return domain.NewError(403, "api_key_forbidden", "当前角色不能撤销 API Key")
	}
	if strings.HasPrefix(keyID, "gw_") {
		if s.accountGateway == nil {
			return domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
		}
		id, err := strconv.Atoi(strings.TrimPrefix(keyID, "gw_"))
		if err != nil || id < 1 {
			return domain.NewError(400, "api_key_invalid", "无效的 Key ID")
		}
		return gatewayAccountError(s.accountGateway.RevokeKey(ctx, organization.OrganizationID, id))
	}
	return s.repository.RevokeAPIKey(ctx, organization.OrganizationID, keyID)
}

func (s *Service) ListModels(ctx context.Context, subject string) ([]domain.Model, error) {
	if _, _, _, err := s.activeOrganization(ctx, subject); err != nil {
		return nil, err
	}
	published, err := s.publishedGatewayModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(published))
	for _, item := range published {
		category := ""
		if len(item.Categories) > 0 {
			category = item.Categories[0]
		}
		models = append(models, domain.Model{
			ID: item.ID, Name: item.Name, Provider: item.Provider,
			Type: category, Context: item.Context, Status: "网关已列出",
			InputPrice: item.InputPrice, OutputPrice: item.OutputPrice,
			CachePrice: item.CachePrice, PriceUnit: item.PriceUnit,
		})
	}
	return models, nil
}

func (s *Service) ListAPITasks(ctx context.Context, subject string) ([]domain.APITask, error) {
	if _, _, _, err := s.activeOrganization(ctx, subject); err != nil {
		return nil, err
	}
	return []domain.APITask{}, nil
}

func (s *Service) Usage(ctx context.Context, subject string) (domain.UsageSummary, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.UsageSummary{}, err
	}
	if s.accountGateway == nil {
		return domain.UsageSummary{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	balance, err := s.accountGateway.Balance(ctx, organization.OrganizationID)
	if err != nil {
		return domain.UsageSummary{}, gatewayAccountError(err)
	}
	budget := float64(balance.RemainingQuota+balance.UsedQuota) / 500000
	used := float64(balance.UsedQuota) / 500000
	remaining := float64(balance.RemainingQuota) / 500000
	percentage := 0.0
	if budget > 0 {
		percentage = used / budget * 100
	}
	return domain.UsageSummary{Budget: budget, Used: used, Remaining: remaining, Percentage: percentage, ByModel: map[string]int{}}, nil
}

func (s *Service) Organization(ctx context.Context, subject string) (domain.Organization, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	return organization, err
}

func (s *Service) UpdateOrganization(ctx context.Context, subject string, input UpdateOrganizationInput) (domain.Organization, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Organization{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return domain.Organization{}, domain.NewError(403, "organization_update_forbidden", "当前角色不能修改组织信息")
	}
	if input.Name != "" && utf8.RuneCountInString(strings.TrimSpace(input.Name)) < 2 {
		return domain.Organization{}, domain.NewError(400, "organization_name_invalid", "组织名称至少需要 2 个字符")
	}
	if input.BillingEmail != "" {
		if _, err := mail.ParseAddress(input.BillingEmail); err != nil {
			return domain.Organization{}, domain.NewError(400, "billing_email_invalid", "账单联系邮箱格式不正确")
		}
	}
	update := domain.Organization{Name: strings.TrimSpace(input.Name), ShortName: strings.TrimSpace(input.ShortName), DefaultRegion: strings.TrimSpace(input.DefaultRegion), Industry: strings.TrimSpace(input.Industry), BillingEmail: strings.TrimSpace(input.BillingEmail)}
	return s.repository.UpdateOrganization(ctx, organization.OrganizationID, update)
}

func (s *Service) ListMembers(ctx context.Context, subject string) ([]domain.Member, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	return s.repository.ListMembers(ctx, organization.OrganizationID)
}

func (s *Service) InviteMember(ctx context.Context, subject string, input InviteMemberInput) (domain.Member, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Member{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return domain.Member{}, domain.NewError(403, "member_invite_forbidden", "当前角色不能邀请成员")
	}
	return s.createMember(ctx, organization.OrganizationID, input)
}

func (s *Service) Billing(ctx context.Context, subject string) (domain.BillingSummary, error) {
	_, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.BillingSummary{}, err
	}
	if !hasAnyRole(roles, "organization_admin", "billing_viewer") {
		return domain.BillingSummary{}, domain.NewError(403, "billing_forbidden", "当前角色不能查看账单")
	}
	return domain.BillingSummary{}, domain.NewError(503, "billing_unavailable", "真实账单数据尚未接入")
}

func (s *Service) ListReleases(ctx context.Context, subject string) ([]domain.Release, error) {
	_, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	if !hasAnyRole(roles, "app_ops_admin") {
		return nil, domain.NewError(403, "release_forbidden", "当前角色不能访问应用发布")
	}
	return s.repository.ListReleases(ctx)
}

func (s *Service) ListOperationsOrganizations(ctx context.Context, subject string) ([]domain.OperationsOrganization, error) {
	_, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	if !hasAnyRole(roles, "customer_success_admin") {
		return nil, domain.NewError(403, "customer_operations_forbidden", "当前角色不能访问客户运营")
	}
	return s.repository.ListOperationsOrganizations(ctx)
}

func (s *Service) activeOrganization(ctx context.Context, subject string) (domain.Organization, []string, string, error) {
	if strings.TrimSpace(subject) == "" {
		return domain.Organization{}, nil, "", domain.NewError(401, "unauthorized", "未建立 Login 会话")
	}
	return s.repository.ActiveOrganization(ctx, subject)
}

func hasAnyRole(actual []string, required ...string) bool {
	for _, role := range required {
		if slices.Contains(actual, role) {
			return true
		}
	}
	return false
}

func newID(prefix string) string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + strings.ToUpper(base64.RawURLEncoding.EncodeToString(bytes))
}
