package control

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type UpdateMemberInput struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

var memberRoles = []string{"成员", "开发者", "财务查看者", "组织管理员"}

type GuestPage struct {
	Users      []gateway.LoginDirectoryUser `json:"users"`
	NextCursor string                       `json:"nextCursor"`
}

type BindMemberInput struct {
	LoginUserID string `json:"loginUserId"`
	Role        string `json:"role"`
}

func (s *Service) ListGuests(ctx context.Context, subject, cursor, query string, limit int) (GuestPage, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return GuestPage{}, err
	}
	if s.loginDirectory == nil {
		return GuestPage{}, domain.NewError(503, "login_directory_unavailable", "Login 用户目录尚未配置")
	}
	if limit < 1 || limit > 100 || len(query) > 160 || len(cursor) > 128 {
		return GuestPage{}, domain.NewError(400, "guest_query_invalid", "游客查询参数无效")
	}
	page, err := s.loginDirectory.ListUsers(ctx, cursor, strings.TrimSpace(query), limit)
	if err != nil {
		return GuestPage{}, domain.NewError(503, "login_directory_unavailable", "Login 用户目录暂时不可用")
	}
	bound, err := s.repository.BoundLoginSubjects(ctx)
	if err != nil {
		return GuestPage{}, err
	}
	result := GuestPage{Users: make([]gateway.LoginDirectoryUser, 0, len(page.Users)), NextCursor: page.NextCursor}
	for _, user := range page.Users {
		if user.ID != "" && user.EmailVerified && user.Status == "active" && !bound[user.ID] {
			result.Users = append(result.Users, user)
		}
	}
	return result, nil
}

func (s *Service) BindManagedMember(ctx context.Context, subject, organizationID string, input BindMemberInput) (domain.Member, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return domain.Member{}, err
	}
	if s.loginDirectory == nil {
		return domain.Member{}, domain.NewError(503, "login_directory_unavailable", "Login 用户目录尚未配置")
	}
	if strings.TrimSpace(input.LoginUserID) == "" || len(input.LoginUserID) > 128 || !slices.Contains(memberRoles, input.Role) {
		return domain.Member{}, domain.NewError(400, "member_bind_invalid", "用户或组织角色无效")
	}
	user, err := s.loginDirectory.User(ctx, input.LoginUserID)
	if err != nil {
		var directoryError gateway.LoginDirectoryHTTPError
		if errors.As(err, &directoryError) && directoryError.Status == http.StatusNotFound {
			return domain.Member{}, domain.NewError(409, "login_user_ineligible", "Login 用户不存在或已删除")
		}
		return domain.Member{}, domain.NewError(503, "login_directory_unavailable", "无法验证 Login 用户")
	}
	if user.ID != input.LoginUserID || user.Status != "active" || !user.EmailVerified || user.Email == "" {
		return domain.Member{}, domain.NewError(409, "login_user_ineligible", "用户未完成邮箱验证或账号已停用")
	}
	return s.repository.BindLoginUser(ctx, organizationID, user.ID, user.Email, input.Role)
}

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
