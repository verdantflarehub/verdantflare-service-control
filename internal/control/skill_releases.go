package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillrelease"
)

// SkillReleaseView is what a member sees: the current published release, or why none is available.
type SkillReleaseView struct {
	Available bool                 `json:"available"`
	Reason    string               `json:"reason,omitempty"`
	Release   *domain.SkillRelease `json:"release,omitempty"`
}

func (s *Service) requireSkillOperator(ctx context.Context, subject string) (domain.Organization, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Organization{}, err
	}
	if !hasAnyRole(roles, "skill_ops_admin") {
		return domain.Organization{}, domain.NewError(403, "operations_forbidden", "当前角色不能执行此运营操作")
	}
	if organization.Status == "冻结" {
		return domain.Organization{}, domain.NewError(403, "organization_frozen", "当前组织已冻结，不能管理技能发行")
	}
	return organization, nil
}

// requireSkillDownloader allows any member of an active, unfrozen organization.
func (s *Service) requireSkillDownloader(ctx context.Context, subject string) (domain.Organization, error) {
	organization, _, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.Organization{}, err
	}
	if organization.Status == "冻结" {
		return domain.Organization{}, domain.NewError(403, "organization_frozen", "当前组织已冻结，不能下载技能")
	}
	return organization, nil
}

func (s *Service) UploadSkillRelease(ctx context.Context, subject string, archive []byte) (domain.SkillRelease, bool, error) {
	organization, err := s.requireSkillOperator(ctx, subject)
	if err != nil {
		return domain.SkillRelease{}, false, err
	}
	input, err := skillrelease.ParseHandoffInput(archive)
	if err != nil {
		return domain.SkillRelease{}, false, domain.NewError(422, "skill_release_invalid", "技能发行包校验失败："+err.Error())
	}
	release := domain.SkillRelease{ReleaseID: input.ReleaseID, Stage: input.Stage, Authentication: input.Authentication,
		ManifestSHA256: input.ManifestSHA256, ArchiveSHA256: input.ArchiveSHA256}
	for _, f := range input.Files {
		release.Files = append(release.Files, domain.SkillReleaseFile{Name: f.Name, Kind: f.Kind, SkillID: f.SkillID,
			Requires: f.Requires, SHA256: f.SHA256, Size: f.Size, Data: f.Data})
	}
	return s.repository.CreateSkillRelease(ctx, release, subject, organization.OrganizationID)
}

func (s *Service) ListManagedSkillReleases(ctx context.Context, subject string) ([]domain.SkillRelease, error) {
	if _, err := s.requireSkillOperator(ctx, subject); err != nil {
		return nil, err
	}
	return s.repository.ListSkillReleases(ctx)
}

func (s *Service) SetSkillReleaseStatus(ctx context.Context, subject, releaseID, status string) (domain.SkillRelease, error) {
	organization, err := s.requireSkillOperator(ctx, subject)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	return s.repository.SetSkillReleaseStatus(ctx, releaseID, status, subject, organization.OrganizationID)
}

// currentSkillRelease is the most recently published release that has not been withdrawn.
func (s *Service) currentSkillRelease(ctx context.Context) (*domain.SkillRelease, error) {
	items, err := s.repository.ListSkillReleases(ctx)
	if err != nil {
		return nil, err
	}
	var current *domain.SkillRelease
	for i := range items {
		item := items[i]
		if item.Status != "published" || item.PublishedAt == nil {
			continue
		}
		if current == nil || item.PublishedAt.After(*current.PublishedAt) {
			current = &item
		}
	}
	return current, nil
}

func (s *Service) CurrentSkillRelease(ctx context.Context, subject string) (SkillReleaseView, error) {
	if _, err := s.requireSkillDownloader(ctx, subject); err != nil {
		return SkillReleaseView{}, err
	}
	current, err := s.currentSkillRelease(ctx)
	if err != nil {
		return SkillReleaseView{}, err
	}
	if current == nil {
		return SkillReleaseView{Reason: "no_published_release"}, nil
	}
	return SkillReleaseView{Available: true, Release: current}, nil
}

// DownloadSkillReleaseFile re-checks membership and release status on every request
// and refuses bytes whose digest no longer matches the stored record.
func (s *Service) DownloadSkillReleaseFile(ctx context.Context, subject, releaseID, name string) (domain.SkillReleaseFile, error) {
	organization, err := s.requireSkillDownloader(ctx, subject)
	if err != nil {
		return domain.SkillReleaseFile{}, err
	}
	items, err := s.repository.ListSkillReleases(ctx)
	if err != nil {
		return domain.SkillReleaseFile{}, err
	}
	status := ""
	for _, item := range items {
		if item.ReleaseID == releaseID {
			status = item.Status
		}
	}
	switch status {
	case "":
		return domain.SkillReleaseFile{}, domain.NewError(404, "skill_release_not_found", "技能发行不存在")
	case "withdrawn":
		return domain.SkillReleaseFile{}, domain.NewError(410, "skill_release_withdrawn", "该发行已撤回，请下载当前版本")
	case "candidate":
		return domain.SkillReleaseFile{}, domain.NewError(404, "skill_release_not_found", "技能发行不存在")
	}
	file, err := s.repository.SkillReleaseFile(ctx, releaseID, name)
	if err != nil {
		return domain.SkillReleaseFile{}, err
	}
	sum := sha256.Sum256(file.Data)
	if hex.EncodeToString(sum[:]) != file.SHA256 || int64(len(file.Data)) != file.Size {
		return domain.SkillReleaseFile{}, domain.NewError(502, "skill_release_corrupted", "技能文件校验失败，请联系运营")
	}
	_ = s.repository.RecordSkillReleaseEvent(ctx, domain.SkillReleaseAuditEvent{ReleaseID: releaseID, Action: "download",
		FileName: name, ActorSubject: subject, OrganizationID: organization.OrganizationID})
	return file, nil
}
