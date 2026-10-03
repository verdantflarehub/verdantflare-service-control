package control

import (
	"context"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

var appIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type ManagedAppInput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Summary       string `json:"summary"`
	Version       string `json:"version"`
	GPU           string `json:"gpu"`
	Duration      string `json:"duration"`
	Icon          string `json:"icon"`
	Tone          string `json:"tone"`
	Channel       string `json:"channel"`
	Developer     string `json:"developer"`
	Description   string `json:"description"`
	Memory        string `json:"memory"`
	Disk          string `json:"disk"`
	CPU           string `json:"cpu"`
	PublicIconURL string `json:"publicIconUrl"`
	PublicVisible bool   `json:"publicVisible"`
}

type CreateManagedOrganizationInput struct {
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Plan      string `json:"plan"`
}

type UpdateManagedOrganizationInput struct {
	Name                       string   `json:"name"`
	ShortName                  string   `json:"shortName"`
	DefaultRegion              string   `json:"defaultRegion"`
	Industry                   string   `json:"industry"`
	BillingEmail               string   `json:"billingEmail"`
	Plan                       string   `json:"plan"`
	Status                     string   `json:"status"`
	AppIDs                     []string `json:"appIds"`
	ExpectedEntitlementVersion int      `json:"expectedEntitlementVersion"`
}

func (s *Service) requireOperationsRole(ctx context.Context, subject, role string) error {
	_, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return err
	}
	if !hasAnyRole(roles, role) {
		return domain.NewError(403, "operations_forbidden", "当前角色不能执行此运营操作")
	}
	return nil
}

func validateManagedApp(input ManagedAppInput) error {
	if utf8.RuneCountInString(strings.TrimSpace(input.Name)) < 2 || strings.TrimSpace(input.Version) == "" {
		return domain.NewError(400, "app_invalid", "应用名称至少 2 个字符，版本不能为空")
	}
	if !slices.Contains([]string{"Candidate", "Preview", "Stable", "Paused"}, input.Channel) {
		return domain.NewError(400, "channel_invalid", "不支持的发布通道")
	}
	if input.PublicIconURL != "" {
		parsed, err := url.Parse(input.PublicIconURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return domain.NewError(400, "icon_url_invalid", "公开图标须为 HTTPS URL")
		}
	}
	return nil
}

func appFromInput(input ManagedAppInput) domain.App {
	icon, tone := strings.TrimSpace(input.Icon), strings.TrimSpace(input.Tone)
	if icon == "" {
		icon = "market"
	}
	if tone == "" {
		tone = "mint"
	}
	return domain.App{ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), Category: strings.TrimSpace(input.Category), Summary: strings.TrimSpace(input.Summary), Version: strings.TrimSpace(input.Version), GPU: strings.TrimSpace(input.GPU), Duration: strings.TrimSpace(input.Duration), Icon: icon, Tone: tone, Channel: input.Channel, Developer: strings.TrimSpace(input.Developer), Description: strings.TrimSpace(input.Description), Memory: strings.TrimSpace(input.Memory), Disk: strings.TrimSpace(input.Disk), CPU: strings.TrimSpace(input.CPU), PublicIconURL: strings.TrimSpace(input.PublicIconURL), PublicVisible: input.PublicVisible}
}

func (s *Service) CreateManagedApp(ctx context.Context, subject string, input ManagedAppInput) (domain.ManagedApp, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.ManagedApp{}, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Channel = "Candidate"
	input.PublicVisible = false
	if !appIDPattern.MatchString(input.ID) {
		return domain.ManagedApp{}, domain.NewError(400, "app_id_invalid", "应用 ID 须为 2–63 位小写字母、数字或连字符")
	}
	if err := validateManagedApp(input); err != nil {
		return domain.ManagedApp{}, err
	}
	return s.repository.CreateManagedApp(ctx, appFromInput(input))
}

func (s *Service) ManagedApp(ctx context.Context, subject, id string) (domain.ManagedApp, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.ManagedApp{}, err
	}
	return s.repository.ManagedApp(ctx, id)
}

func (s *Service) UpdateManagedApp(ctx context.Context, subject, id string, input ManagedAppInput) (domain.ManagedApp, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.ManagedApp{}, err
	}
	if err := validateManagedApp(input); err != nil {
		return domain.ManagedApp{}, err
	}
	if input.PublicVisible && !slices.Contains([]string{"Preview", "Stable"}, input.Channel) {
		return domain.ManagedApp{}, domain.NewError(400, "public_app_unpublished", "仅 Preview 或 Stable 应用可公开展示")
	}
	return s.repository.UpdateManagedApp(ctx, id, appFromInput(input))
}

func (s *Service) CreateManagedOrganization(ctx context.Context, subject string, input CreateManagedOrganizationInput) (domain.ManagedOrganization, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.ManagedOrganization{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(input.Name) < 2 || !slices.Contains([]string{"Pilot", "Studio", "Enterprise"}, input.Plan) {
		return domain.ManagedOrganization{}, domain.NewError(400, "organization_invalid", "组织名称或套餐无效")
	}
	shortName := strings.TrimSpace(input.ShortName)
	if shortName == "" {
		shortName = string([]rune(input.Name)[0])
	}
	return s.repository.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: newID("org"), Name: input.Name, ShortName: shortName, Plan: input.Plan, DefaultRegion: "cn-east-1"})
}

func (s *Service) ManagedOrganization(ctx context.Context, subject, id string) (domain.ManagedOrganization, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.ManagedOrganization{}, err
	}
	return s.repository.ManagedOrganization(ctx, id)
}

func (s *Service) UpdateManagedOrganization(ctx context.Context, subject, id string, input UpdateManagedOrganizationInput) (domain.ManagedOrganization, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.ManagedOrganization{}, err
	}
	if !slices.Contains([]string{"Pilot", "Studio", "Enterprise"}, input.Plan) || !slices.Contains([]string{"正常", "冻结"}, input.Status) || input.AppIDs == nil || input.ExpectedEntitlementVersion < 1 {
		return domain.ManagedOrganization{}, domain.NewError(400, "organization_update_invalid", "套餐、状态、权益或版本无效")
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.Industry = strings.TrimSpace(input.Industry)
	input.BillingEmail = strings.TrimSpace(input.BillingEmail)
	if input.Name != "" && utf8.RuneCountInString(input.Name) < 2 {
		return domain.ManagedOrganization{}, domain.NewError(400, "organization_name_invalid", "组织名称至少需要 2 个字符")
	}
	if input.DefaultRegion != "" && !slices.Contains([]string{"cn-east-1", "cn-north-1"}, input.DefaultRegion) {
		return domain.ManagedOrganization{}, domain.NewError(400, "organization_region_invalid", "默认区域无效")
	}
	if input.BillingEmail != "" {
		address, err := mail.ParseAddress(input.BillingEmail)
		if err != nil || address.Name != "" {
			return domain.ManagedOrganization{}, domain.NewError(400, "billing_email_invalid", "账单邮箱格式不正确")
		}
	}
	return s.repository.UpdateManagedOrganization(ctx, id, domain.Organization{Name: input.Name, ShortName: input.ShortName, DefaultRegion: input.DefaultRegion, Industry: input.Industry, BillingEmail: input.BillingEmail, Plan: input.Plan, Status: input.Status}, input.AppIDs, input.ExpectedEntitlementVersion)
}
