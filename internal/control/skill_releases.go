package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

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

// SkillSource fetches release inputs from the trusted skills repository.
type SkillSource interface {
	Repository() string
	Fetch(ctx context.Context, releaseID, expectedArchiveSHA256 string) ([]byte, domain.SkillReleaseSource, error)
}

// SetSkillSource configures where releases may be registered from. Without
// it registration is refused; listing, publishing and withdrawing still work.
func (s *Service) SetSkillSource(source SkillSource) { s.skillSource = source }

// SkillActor identifies the caller of a release change. TokenID and Tool are
// empty for the Hub page and set for the skill publishing MCP.
type SkillActor struct {
	Subject string
	TokenID string
	Tool    string
}

func (s *Service) skillReleaseActor(ctx context.Context, actor SkillActor) (domain.SkillReleaseActor, error) {
	organization, err := s.requireSkillOperator(ctx, actor.Subject)
	if err != nil {
		return domain.SkillReleaseActor{}, err
	}
	return domain.SkillReleaseActor{Subject: actor.Subject, OrganizationID: organization.OrganizationID, TokenID: actor.TokenID, Tool: actor.Tool}, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func checkReleaseArguments(releaseID, expectedSHA256 string, digestRequired bool) error {
	if !skillrelease.ValidReleaseID(releaseID) {
		return domain.NewError(400, "skill_release_id_invalid", "发行号无效")
	}
	if (digestRequired || expectedSHA256 != "") && !validSHA256(expectedSHA256) {
		return domain.NewError(400, "skill_release_digest_invalid", "expectedArchiveSha256 须为 64 位小写十六进制 SHA-256")
	}
	return nil
}

// RegisterSkillRelease fetches vf-hub-skill-input-<releaseID> from the skills
// repository and stores it as a candidate. Registering the same id and digest
// again returns the existing release without contacting GitHub.
func (s *Service) RegisterSkillRelease(ctx context.Context, caller SkillActor, releaseID, expectedSHA256 string) (domain.SkillRelease, bool, error) {
	actor, err := s.skillReleaseActor(ctx, caller)
	if err != nil {
		return domain.SkillRelease{}, false, err
	}
	if err := checkReleaseArguments(releaseID, expectedSHA256, true); err != nil {
		return domain.SkillRelease{}, false, err
	}
	if existing, err := s.repository.GetSkillRelease(ctx, releaseID); err == nil {
		if existing.ArchiveSHA256 != expectedSHA256 {
			return domain.SkillRelease{}, false, domain.NewError(409, "skill_release_conflict", "该发行号已登记为另一份发行包，不能覆盖；新内容请使用新发行号")
		}
		return existing, false, nil
	} else if !isNotFound(err) {
		return domain.SkillRelease{}, false, err
	}
	if s.skillSource == nil {
		return domain.SkillRelease{}, false, domain.NewError(503, "skill_source_unconfigured", "Control 未配置技能仓库来源，暂不能登记发行")
	}
	archive, source, err := s.skillSource.Fetch(ctx, releaseID, expectedSHA256)
	if err != nil {
		return domain.SkillRelease{}, false, err
	}
	input, err := skillrelease.ParseHandoffInput(archive)
	if err != nil {
		return domain.SkillRelease{}, false, domain.NewError(422, "skill_release_invalid", "技能发行包校验失败："+err.Error())
	}
	switch {
	case input.ReleaseID != releaseID:
		return domain.SkillRelease{}, false, domain.NewError(422, "skill_release_invalid", "发行包内的发行号与请求不一致")
	case input.SourceDirty:
		return domain.SkillRelease{}, false, domain.NewError(422, "skill_source_untrusted", "来源核对未通过：发行包由未提交的工作区构建（dirty=true）")
	case input.SourceCommit != source.Commit:
		return domain.SkillRelease{}, false, domain.NewError(422, "skill_source_untrusted", "来源核对未通过：发行包的来源锁提交与标签提交不一致")
	}
	release := domain.SkillRelease{ReleaseID: input.ReleaseID, Stage: input.Stage, Authentication: input.Authentication,
		ManifestSHA256: input.ManifestSHA256, ArchiveSHA256: input.ArchiveSHA256, Source: &source}
	for _, f := range input.Files {
		release.Files = append(release.Files, domain.SkillReleaseFile{Name: f.Name, Kind: f.Kind, SkillID: f.SkillID,
			Requires: f.Requires, SHA256: f.SHA256, Size: f.Size, Data: f.Data})
	}
	return s.repository.CreateSkillRelease(ctx, release, actor)
}

func isNotFound(err error) bool {
	var failure *domain.Error
	return errors.As(err, &failure) && failure.Status == 404
}

// SkillReleaseList is the operator view: newest releases and the current pointer.
type SkillReleaseList struct {
	Items            []domain.SkillRelease `json:"items"`
	CurrentReleaseID string                `json:"currentReleaseId"`
}

func (s *Service) ListManagedSkillReleases(ctx context.Context, subject string) (SkillReleaseList, error) {
	if _, err := s.requireSkillOperator(ctx, subject); err != nil {
		return SkillReleaseList{}, err
	}
	items, err := s.repository.ListSkillReleases(ctx)
	if err != nil {
		return SkillReleaseList{}, err
	}
	return SkillReleaseList{Items: items, CurrentReleaseID: currentReleaseID(items)}, nil
}

// SkillReleaseReadiness is the publish gate. The Hub page and the MCP both go
// through it, so they always reach the same verdict for a candidate.
type SkillReleaseReadiness struct {
	ReleaseID       string   `json:"releaseId"`
	Status          string   `json:"status"`
	ArchiveSHA256   string   `json:"archiveSha256"`
	Ready           bool     `json:"ready"`
	ContentVerified bool     `json:"contentVerified"`
	SourceVerified  bool     `json:"sourceVerified"`
	Missing         []string `json:"missing"`
}

func (s *Service) readiness(ctx context.Context, release domain.SkillRelease) (SkillReleaseReadiness, error) {
	result := SkillReleaseReadiness{ReleaseID: release.ReleaseID, Status: release.Status, ArchiveSHA256: release.ArchiveSHA256, Missing: []string{}}
	if release.Status != "candidate" {
		result.Missing = append(result.Missing, "status_"+release.Status)
	}
	source := release.Source
	switch {
	case source == nil:
		result.Missing = append(result.Missing, "source_receipt_missing")
	case s.skillSource == nil || source.Repository != s.skillSource.Repository():
		result.Missing = append(result.Missing, "source_repository_not_trusted")
	case source.SHA256 != release.ArchiveSHA256:
		result.Missing = append(result.Missing, "source_digest_mismatch")
	default:
		result.SourceVerified = true
	}
	result.ContentVerified = true
	for _, meta := range release.Files {
		file, err := s.repository.SkillReleaseFile(ctx, release.ReleaseID, meta.Name)
		if err != nil {
			return SkillReleaseReadiness{}, err
		}
		sum := sha256.Sum256(file.Data)
		if hex.EncodeToString(sum[:]) != meta.SHA256 || int64(len(file.Data)) != meta.Size {
			result.ContentVerified = false
		}
	}
	if len(release.Files) == 0 {
		result.ContentVerified = false
	}
	if !result.ContentVerified {
		result.Missing = append(result.Missing, "content_digest_mismatch")
	}
	result.Ready = len(result.Missing) == 0
	return result, nil
}

func (s *Service) managedSkillRelease(ctx context.Context, releaseID, expectedSHA256 string, digestRequired bool) (domain.SkillRelease, error) {
	if err := checkReleaseArguments(releaseID, expectedSHA256, digestRequired); err != nil {
		return domain.SkillRelease{}, err
	}
	release, err := s.repository.GetSkillRelease(ctx, releaseID)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	if expectedSHA256 != "" && release.ArchiveSHA256 != expectedSHA256 {
		return domain.SkillRelease{}, domain.NewError(409, "skill_release_digest_mismatch", "发行包 SHA-256 与预期不符，请先回读该发行")
	}
	return release, nil
}

func (s *Service) SkillReleaseReadinessFor(ctx context.Context, subject, releaseID string) (SkillReleaseReadiness, error) {
	if _, err := s.requireSkillOperator(ctx, subject); err != nil {
		return SkillReleaseReadiness{}, err
	}
	release, err := s.managedSkillRelease(ctx, releaseID, "", false)
	if err != nil {
		return SkillReleaseReadiness{}, err
	}
	return s.readiness(ctx, release)
}

// SkillReleaseDetail is what an Agent reads back after an unclear result.
type SkillReleaseDetail struct {
	Release          domain.SkillRelease             `json:"release"`
	Readiness        SkillReleaseReadiness           `json:"readiness"`
	CurrentReleaseID string                          `json:"currentReleaseId"`
	Audit            []domain.SkillReleaseAuditEvent `json:"audit"`
}

func (s *Service) SkillReleaseDetailFor(ctx context.Context, subject, releaseID string) (SkillReleaseDetail, error) {
	if _, err := s.requireSkillOperator(ctx, subject); err != nil {
		return SkillReleaseDetail{}, err
	}
	release, err := s.managedSkillRelease(ctx, releaseID, "", false)
	if err != nil {
		return SkillReleaseDetail{}, err
	}
	readiness, err := s.readiness(ctx, release)
	if err != nil {
		return SkillReleaseDetail{}, err
	}
	items, err := s.repository.ListSkillReleases(ctx)
	if err != nil {
		return SkillReleaseDetail{}, err
	}
	audit, err := s.repository.SkillReleaseAudit(ctx, releaseID, 20)
	if err != nil {
		return SkillReleaseDetail{}, err
	}
	return SkillReleaseDetail{Release: release, Readiness: readiness, CurrentReleaseID: currentReleaseID(items), Audit: audit}, nil
}

// PublishSkillRelease runs the publish gate and then makes the release
// current. expectedSHA256 may be empty only for the Hub page.
func (s *Service) PublishSkillRelease(ctx context.Context, caller SkillActor, releaseID, expectedSHA256 string) (domain.SkillRelease, error) {
	actor, err := s.skillReleaseActor(ctx, caller)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	release, err := s.managedSkillRelease(ctx, releaseID, expectedSHA256, caller.TokenID != "")
	if err != nil {
		return domain.SkillRelease{}, err
	}
	if release.Status == "candidate" {
		readiness, err := s.readiness(ctx, release)
		if err != nil {
			return domain.SkillRelease{}, err
		}
		if !readiness.Ready {
			return domain.SkillRelease{}, domain.NewError(422, "skill_release_not_ready", "发行未通过发布检查："+strings.Join(readiness.Missing, ", "))
		}
	}
	// The store re-checks the transition atomically (published／withdrawn give 409).
	return s.repository.SetSkillReleaseStatus(ctx, releaseID, "published", "", actor)
}

// WithdrawSkillRelease is not gated on the source: a bad release must always be removable.
func (s *Service) WithdrawSkillRelease(ctx context.Context, caller SkillActor, releaseID, expectedSHA256, reason string) (domain.SkillRelease, error) {
	actor, err := s.skillReleaseActor(ctx, caller)
	if err != nil {
		return domain.SkillRelease{}, err
	}
	mcp := caller.TokenID != ""
	if _, err := s.managedSkillRelease(ctx, releaseID, expectedSHA256, mcp); err != nil {
		return domain.SkillRelease{}, err
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 200 || mcp && utf8.RuneCountInString(reason) < 2 {
		return domain.SkillRelease{}, domain.NewError(400, "skill_release_reason_invalid", "撤回原因须为 2–200 个字符")
	}
	return s.repository.SetSkillReleaseStatus(ctx, releaseID, "withdrawn", reason, actor)
}

func currentReleaseID(items []domain.SkillRelease) string {
	var current *domain.SkillRelease
	for i := range items {
		item := items[i]
		if item.Status == "published" && item.PublishedAt != nil && (current == nil || item.PublishedAt.After(*current.PublishedAt)) {
			current = &item
		}
	}
	if current == nil {
		return ""
	}
	return current.ReleaseID
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
