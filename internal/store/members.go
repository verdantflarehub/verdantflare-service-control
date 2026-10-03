package store

import (
	"context"
	"sort"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func memberRole(roles []string) string {
	for _, candidate := range []struct{ key, label string }{
		{"organization_admin", "组织管理员"}, {"billing_viewer", "财务查看者"},
		{"developer", "开发者"}, {"member", "成员"},
	} {
		for _, role := range roles {
			if role == candidate.key {
				return candidate.label
			}
		}
	}
	return "成员"
}

func memberRoleKey(label string) string {
	switch label {
	case "组织管理员":
		return "organization_admin"
	case "财务查看者":
		return "billing_viewer"
	case "开发者":
		return "developer"
	default:
		return "member"
	}
}

func (m *Memory) membersForLocked(organizationID string) []domain.Member {
	result := make([]domain.Member, 0, len(m.members[organizationID])+len(m.users))
	boundEmails := map[string]bool{}
	for subject, user := range m.users {
		membership, ok := membershipFor(user, organizationID)
		if !ok {
			continue
		}
		status := "正常"
		if membership.Status == "停用" {
			status = "停用"
		}
		name := user.DisplayName
		if name == "" {
			name = strings.Split(user.Email, "@")[0]
		}
		avatar := "?"
		if name != "" {
			avatar = strings.ToUpper(string([]rune(name)[0]))
		}
		result = append(result, domain.Member{ID: subject, CenterUserID: user.CenterUserID, Name: name, Email: user.Email, Role: memberRole(membership.Roles), Joined: "已绑定 Login", Status: status, Avatar: avatar})
		boundEmails[strings.ToLower(user.Email)] = true
	}
	for _, member := range m.members[organizationID] {
		if boundEmails[strings.ToLower(member.Email)] {
			continue
		}
		if member.ID == "" {
			member.ID = "legacy:" + strings.ToLower(member.Email)
		}
		if member.Status == "正常" {
			member.Status = "未绑定"
		}
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Email < result[j].Email })
	return result
}

func (m *Memory) UpdateMember(_ context.Context, organizationID, memberID, role, status, actorSubject string) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.organizations[organizationID]; !ok {
		return domain.Member{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	if user, ok := m.users[memberID]; ok {
		if memberID == actorSubject {
			return domain.Member{}, domain.NewError(409, "self_change_forbidden", "不能修改自己的角色或状态")
		}
		found := false
		for i, membership := range user.Memberships {
			if membership.OrganizationID != organizationID {
				continue
			}
			found = true
			if status != "正常" && status != "停用" {
				return domain.Member{}, domain.NewError(400, "member_status_invalid", "已绑定用户只能设为正常或停用")
			}
			wasAdmin := memberRole(membership.Roles) == "组织管理员" && membership.Status != "停用"
			willBeAdmin := role == "组织管理员" && status == "正常"
			if wasAdmin && !willBeAdmin && m.activeAdminCountLocked(organizationID) <= 1 {
				return domain.Member{}, domain.NewError(409, "last_admin_forbidden", "不能停用或降级最后一位组织管理员")
			}
			roles := []string{memberRoleKey(role)}
			for _, existing := range membership.Roles {
				if existing != "organization_admin" && existing != "billing_viewer" && existing != "developer" && existing != "member" {
					roles = append(roles, existing)
				}
			}
			user.Memberships[i].Roles = roles
			user.Memberships[i].Status = status
			break
		}
		if !found {
			return domain.Member{}, domain.NewError(404, "member_not_found", "成员不属于该组织")
		}
		m.users[memberID] = user
	} else {
		found := false
		for i, member := range m.members[organizationID] {
			id := member.ID
			if id == "" {
				id = "legacy:" + strings.ToLower(member.Email)
			}
			if id != memberID {
				continue
			}
			if status != "待邀请" && status != "已取消" && status != "未绑定" {
				return domain.Member{}, domain.NewError(400, "member_status_invalid", "未绑定记录只能待邀请、未绑定或取消")
			}
			member.ID, member.Role, member.Status = id, role, status
			m.members[organizationID][i] = member
			found = true
			break
		}
		if !found {
			return domain.Member{}, domain.NewError(404, "member_not_found", "成员记录不存在")
		}
	}
	for _, member := range m.membersForLocked(organizationID) {
		if member.ID == memberID {
			return member, nil
		}
	}
	return domain.Member{}, domain.NewError(404, "member_not_found", "成员记录不存在")
}

func (m *Memory) activeAdminCountLocked(organizationID string) int {
	count := 0
	for _, user := range m.users {
		membership, ok := membershipFor(user, organizationID)
		if ok && membership.Status != "停用" && memberRole(membership.Roles) == "组织管理员" {
			count++
		}
	}
	return count
}
