package control

import (
	"context"
	"net/mail"
	"slices"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type UpdateMemberInput struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

var memberRoles = []string{"成员", "开发者", "财务查看者", "组织管理员"}

func (s *Service) createMember(ctx context.Context, organizationID string, input InviteMemberInput) (domain.Member, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(input.Email))
	if err != nil || address.Name != "" {
		return domain.Member{}, domain.NewError(400, "member_email_invalid", "成员邮箱格式不正确")
	}
	if !slices.Contains(memberRoles, input.Role) {
		return domain.Member{}, domain.NewError(400, "member_role_invalid", "不支持的组织角色")
	}
	local := strings.Split(address.Address, "@")[0]
	member := domain.Member{Name: local, Email: strings.ToLower(address.Address), Role: input.Role, Joined: s.now().Format("2006-01-02"), Status: "待邀请", Avatar: strings.ToUpper(string([]rune(local)[0]))}
	return s.repository.AddMember(ctx, organizationID, member)
}

func (s *Service) UpdateMember(ctx context.Context, subject, memberID string, input UpdateMemberInput) (domain.Member, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Member{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return domain.Member{}, domain.NewError(403, "member_update_forbidden", "当前角色不能管理成员")
	}
	return s.updateMember(ctx, organization.OrganizationID, memberID, subject, input)
}

func (s *Service) CreateManagedMember(ctx context.Context, subject, organizationID string, input InviteMemberInput) (domain.Member, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.Member{}, err
	}
	return s.createMember(ctx, organizationID, input)
}

func (s *Service) UpdateManagedMember(ctx context.Context, subject, organizationID, memberID string, input UpdateMemberInput) (domain.Member, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.Member{}, err
	}
	return s.updateMember(ctx, organizationID, memberID, subject, input)
}

func (s *Service) updateMember(ctx context.Context, organizationID, memberID, actorSubject string, input UpdateMemberInput) (domain.Member, error) {
	if !slices.Contains(memberRoles, input.Role) || !slices.Contains([]string{"正常", "停用", "待邀请", "未绑定", "已取消"}, input.Status) {
		return domain.Member{}, domain.NewError(400, "member_update_invalid", "成员角色或状态无效")
	}
	return s.repository.UpdateMember(ctx, organizationID, memberID, input.Role, input.Status, actorSubject)
}
