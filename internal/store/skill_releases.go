package store

import (
	"bytes"
	"context"
	"sort"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func releaseNotFound() error {
	return domain.NewError(404, "skill_release_not_found", "技能发行不存在")
}

func releaseConflict() error {
	return domain.NewError(409, "skill_release_conflict", "同一发行号已登记为不同内容")
}

func sameReleaseContent(a, b domain.SkillRelease) bool {
	if a.ManifestSHA256 != b.ManifestSHA256 || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i].Name != b.Files[i].Name || a.Files[i].SHA256 != b.Files[i].SHA256 {
			return false
		}
	}
	return true
}

func withoutData(release domain.SkillRelease) domain.SkillRelease {
	files := make([]domain.SkillReleaseFile, len(release.Files))
	for i, f := range release.Files {
		f.Data = nil
		f.Requires = append([]string(nil), f.Requires...)
		files[i] = f
	}
	release.Files = files
	return release
}

// CreateSkillRelease stores a new candidate. The bool reports whether it was newly created;
// re-uploading identical content is idempotent and different content is a conflict.
func (m *Memory) CreateSkillRelease(_ context.Context, release domain.SkillRelease, actor, organizationID string) (domain.SkillRelease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skillReleases == nil {
		m.skillReleases = make(map[string]domain.SkillRelease)
	}
	if current, ok := m.skillReleases[release.ReleaseID]; ok {
		if !sameReleaseContent(current, release) {
			return domain.SkillRelease{}, false, releaseConflict()
		}
		return withoutData(current), false, nil
	}
	release.Status, release.CreatedBy, release.CreatedAt = "candidate", actor, time.Now().UTC()
	files := make([]domain.SkillReleaseFile, len(release.Files))
	for i, f := range release.Files {
		f.Data = bytes.Clone(f.Data)
		files[i] = f
	}
	release.Files = files
	m.skillReleases[release.ReleaseID] = release
	m.skillReleaseAudit = append(m.skillReleaseAudit, domain.SkillReleaseAuditEvent{ReleaseID: release.ReleaseID, Action: "upload", ActorSubject: actor, OrganizationID: organizationID, CreatedAt: release.CreatedAt})
	return withoutData(release), true, nil
}

func (m *Memory) ListSkillReleases(_ context.Context) ([]domain.SkillRelease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.SkillRelease, 0, len(m.skillReleases))
	for _, release := range m.skillReleases {
		items = append(items, withoutData(release))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

// SetSkillReleaseStatus moves a release to published or withdrawn.
func (m *Memory) SetSkillReleaseStatus(_ context.Context, id, status, actor, organizationID string) (domain.SkillRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, ok := m.skillReleases[id]
	if !ok {
		return domain.SkillRelease{}, releaseNotFound()
	}
	if err := checkReleaseTransition(release.Status, status); err != nil {
		return domain.SkillRelease{}, err
	}
	now := time.Now().UTC()
	release.Status = status
	if status == "published" {
		release.PublishedAt = &now
	} else {
		release.WithdrawnAt = &now
	}
	m.skillReleases[id] = release
	m.skillReleaseAudit = append(m.skillReleaseAudit, domain.SkillReleaseAuditEvent{ReleaseID: id, Action: actionFor(status), ActorSubject: actor, OrganizationID: organizationID, CreatedAt: now})
	return withoutData(release), nil
}

func (m *Memory) SkillReleaseFile(_ context.Context, id, name string) (domain.SkillReleaseFile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	release, ok := m.skillReleases[id]
	if !ok {
		return domain.SkillReleaseFile{}, releaseNotFound()
	}
	for _, f := range release.Files {
		if f.Name == name {
			f.Data = bytes.Clone(f.Data)
			return f, nil
		}
	}
	return domain.SkillReleaseFile{}, domain.NewError(404, "skill_release_file_not_found", "发行中没有该文件")
}

func (m *Memory) RecordSkillReleaseEvent(_ context.Context, event domain.SkillReleaseAuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	event.CreatedAt = time.Now().UTC()
	m.skillReleaseAudit = append(m.skillReleaseAudit, event)
	return nil
}

func checkReleaseTransition(from, to string) error {
	switch {
	case to == "published" && from == "candidate", to == "withdrawn" && from == "published":
		return nil
	case to == "published" && from == "published", to == "withdrawn" && from == "withdrawn":
		return domain.NewError(409, "skill_release_unchanged", "发行已处于该状态")
	case to == "published" && from == "withdrawn":
		return domain.NewError(409, "skill_release_withdrawn", "已撤回的发行不能重新发布，请上传新版本")
	case to == "withdrawn" && from == "candidate":
		return domain.NewError(409, "skill_release_not_published", "候选发行尚未发布，无需撤回")
	}
	return domain.NewError(400, "skill_release_status_invalid", "发行状态无效")
}

func actionFor(status string) string {
	if status == "published" {
		return "publish"
	}
	return "withdraw"
}

// GrantSkillOperator adds skill_ops_admin to one existing, active membership.
// It never creates users or memberships; repeating it is a no-op.
func (m *Memory) GrantSkillOperator(_ context.Context, subject, organizationID string) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[subject]
	if !ok {
		return nil, false, domain.NewError(404, "center_user_not_found", "登录主体尚未绑定 Center User")
	}
	for i, membership := range user.Memberships {
		if membership.OrganizationID != organizationID {
			continue
		}
		if membership.Status == "停用" {
			return nil, false, domain.NewError(409, "membership_disabled", "该组织成员资格已停用")
		}
		for _, role := range membership.Roles {
			if role == "skill_ops_admin" {
				return append([]string(nil), membership.Roles...), false, nil
			}
		}
		user.Memberships[i].Roles = append(append([]string(nil), membership.Roles...), "skill_ops_admin")
		m.users[subject] = user
		return append([]string(nil), user.Memberships[i].Roles...), true, nil
	}
	return nil, false, domain.NewError(404, "membership_not_found", "该账号不在指定组织中")
}

// SkillOperatorRoles reports the roles of one membership without changing anything.
func (m *Memory) SkillOperatorRoles(_ context.Context, subject, organizationID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[subject]
	if !ok {
		return nil, domain.NewError(404, "center_user_not_found", "登录主体尚未绑定 Center User")
	}
	for _, membership := range user.Memberships {
		if membership.OrganizationID == organizationID {
			return append([]string(nil), membership.Roles...), nil
		}
	}
	return nil, domain.NewError(404, "membership_not_found", "该账号不在指定组织中")
}
