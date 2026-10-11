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
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	GroupID       string              `json:"groupId"`
	Category      string              `json:"category"`
	Summary       string              `json:"summary"`
	Version       string              `json:"version"`
	GPU           string              `json:"gpu"`
	Duration      string              `json:"duration"`
	Icon          string              `json:"icon"`
	Tone          string              `json:"tone"`
	Channel       string              `json:"channel"`
	Developer     string              `json:"developer"`
	Description   string              `json:"description"`
	Memory        string              `json:"memory"`
	Disk          string              `json:"disk"`
	CPU           string              `json:"cpu"`
	PublicIconURL string              `json:"publicIconUrl"`
	PublicVisible bool                `json:"publicVisible"`
	Showcase      *domain.AppShowcase `json:"showcase"`
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
	showcase := domain.AppShowcase{}
	if input.Showcase != nil {
		showcase = *input.Showcase
	}
	if utf8.RuneCountInString(strings.TrimSpace(input.Name)) < 2 {
		return domain.NewError(400, "app_invalid", "应用名称至少 2 个字符")
	}
	if !slices.Contains([]string{"Candidate", "Listed", "Preview", "Stable", "Paused"}, input.Channel) {
		return domain.NewError(400, "channel_invalid", "不支持的发布通道")
	}
	if input.GroupID != "" && !slices.Contains([]string{"image", "music", "video", "agent"}, input.GroupID) {
		return domain.NewError(400, "app_group_invalid", "应用分组须为 image、music、video 或 agent")
	}
	if slices.Contains([]string{"Preview", "Stable"}, input.Channel) && strings.TrimSpace(input.Version) == "" {
		return domain.NewError(400, "app_version_required", "公开或预览应用的目录版本不能为空；资料草稿可以留空")
	}
	if input.Channel == "Listed" && strings.TrimSpace(input.Version) != "" {
		return domain.NewError(400, "listed_app_version_forbidden", "仅资料上架的应用不得声明交付包版本")
	}
	if input.PublicIconURL != "" {
		parsed, err := url.Parse(input.PublicIconURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return domain.NewError(400, "icon_url_invalid", "公开图标须为 HTTPS URL")
		}
	}
	for _, address := range append(append([]string{}, showcase.Screenshots...), showcase.WebsiteURL, showcase.DocsURL, showcase.SourceURL) {
		if address == "" {
			continue
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return domain.NewError(400, "showcase_url_invalid", "展示图片与外部链接须为 HTTPS URL")
		}
	}
	if len(showcase.Screenshots) > 8 || len(showcase.Highlights) > 12 || len(showcase.Permissions) > 12 {
		return domain.NewError(400, "showcase_limit", "截图最多 8 张，功能与权限最多各 12 条")
	}
	for _, screenshot := range showcase.Screenshots {
		if strings.TrimSpace(screenshot) == "" {
			return domain.NewError(400, "showcase_image_invalid", "截图 URL 不能为空")
		}
	}
	for _, highlight := range showcase.Highlights {
		if strings.TrimSpace(highlight) == "" {
			return domain.NewError(400, "showcase_highlight_invalid", "功能亮点不能为空")
		}
	}
	if input.Channel == "Listed" {
		if strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Summary) == "" || strings.TrimSpace(input.Developer) == "" || strings.TrimSpace(input.Description) == "" || len(showcase.Screenshots) == 0 || len(showcase.Highlights) == 0 {
			return domain.NewError(400, "listing_incomplete", "上架 Hub 目录须填写分类、简介、开发者、应用介绍、至少一张截图及一条功能亮点")
		}
	}
	return nil
}

func appFromInput(input ManagedAppInput) domain.App {
	showcase := domain.AppShowcase{}
	if input.Showcase != nil {
		showcase = *input.Showcase
	}
	icon, tone := strings.TrimSpace(input.Icon), strings.TrimSpace(input.Tone)
	if icon == "" {
		icon = "market"
	}
	if tone == "" {
		tone = "mint"
	}
	return domain.App{ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), GroupID: input.GroupID, Category: strings.TrimSpace(input.Category), Summary: strings.TrimSpace(input.Summary), Version: strings.TrimSpace(input.Version), GPU: strings.TrimSpace(input.GPU), Duration: strings.TrimSpace(input.Duration), Icon: icon, Tone: tone, Channel: input.Channel, Developer: strings.TrimSpace(input.Developer), Description: strings.TrimSpace(input.Description), Memory: strings.TrimSpace(input.Memory), Disk: strings.TrimSpace(input.Disk), CPU: strings.TrimSpace(input.CPU), PublicIconURL: strings.TrimSpace(input.PublicIconURL), PublicVisible: input.PublicVisible, Showcase: showcase}
}

func (s *Service) CreateManagedApp(ctx context.Context, subject string, input ManagedAppInput) (domain.ManagedApp, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.ManagedApp{}, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Channel = "Candidate"
	input.PublicVisible = false
	if input.GroupID != "" && strings.TrimSpace(input.Version) != "" {
		return domain.ManagedApp{}, domain.NewError(400, "app_version_not_registered", "新应用须先登记候选版本，再选择目录展示版本")
	}
	if !appIDPattern.MatchString(input.ID) {
		return domain.ManagedApp{}, domain.NewError(400, "app_id_invalid", "应用 ID 须为 2–63 位小写字母、数字或连字符")
	}
	if input.GroupID != "" && (strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Summary) == "") {
		return domain.ManagedApp{}, domain.NewError(400, "app_basic_info_incomplete", "新应用须填写市场分类和一句话简介")
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
	current, err := s.repository.ManagedApp(ctx, id)
	if err != nil {
		return domain.ManagedApp{}, err
	}
	if input.Showcase == nil {
		input.Showcase = &current.App.Showcase
	}
	if err := validateManagedApp(input); err != nil {
		return domain.ManagedApp{}, err
	}
	if input.GroupID != current.App.GroupID {
		return domain.ManagedApp{}, domain.NewError(409, "app_group_immutable", "已建应用的分组不能改变")
	}
	if current.App.GroupID != "" {
		if !slices.Contains([]string{"Preview", "Stable"}, current.App.Channel) && slices.Contains([]string{"Preview", "Stable"}, input.Channel) {
			return domain.ManagedApp{}, domain.NewError(409, "release_evidence_missing", "Chart 预检不能发布应用；须先接入可信制品、隔离渲染及目标 Station 验证证据")
		}
		if input.Version != "" {
			if _, err := s.repository.AppVersion(ctx, id, input.Version); err != nil {
				return domain.ManagedApp{}, domain.NewError(409, "app_version_not_registered", "目录版本必须选择已登记的候选版本")
			}
		}
	}
	if input.PublicVisible && !slices.Contains([]string{"Preview", "Stable"}, input.Channel) {
		return domain.ManagedApp{}, domain.NewError(400, "public_app_unpublished", "仅 Preview 或 Stable 应用可进入 WWW 公共目录")
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
	current, err := s.repository.ManagedOrganization(ctx, id)
	if err != nil {
		return domain.ManagedOrganization{}, err
	}
	if current.Organization.EntitlementVersion != input.ExpectedEntitlementVersion {
		return domain.ManagedOrganization{}, domain.NewError(409, "entitlement_version_conflict", "权益已被其他管理员修改，请刷新后重试")
	}
	if (input.Status == "冻结" || current.Organization.Status == "冻结") && s.accountGateway == nil {
		return domain.ManagedOrganization{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	if input.Status == "冻结" {
		if err := s.accountGateway.SetEnabled(ctx, id, false); err != nil {
			return domain.ManagedOrganization{}, gatewayAccountError(err)
		}
	}
	result, err := s.repository.UpdateManagedOrganization(ctx, id, domain.Organization{Name: input.Name, ShortName: input.ShortName, DefaultRegion: input.DefaultRegion, Industry: input.Industry, BillingEmail: input.BillingEmail, Plan: input.Plan, Status: input.Status}, input.AppIDs, input.ExpectedEntitlementVersion)
	if err != nil {
		return domain.ManagedOrganization{}, err
	}
	if input.Status == "正常" && s.accountGateway != nil {
		if err := s.accountGateway.SetEnabled(ctx, id, true); err != nil {
			return result, gatewayAccountError(err)
		}
	}
	return result, nil
}
