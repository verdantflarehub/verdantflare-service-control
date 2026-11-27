package control

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type PatchSkillEntryInput struct {
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Category    string `json:"category"`
	SortOrder   int    `json:"sortOrder"`
	Visibility  string `json:"visibility"`
}

func validateSkillEntryInput(input PatchSkillEntryInput) error {
	for _, field := range []string{input.Name, input.Summary, input.Description, input.Category} {
		if strings.TrimSpace(field) == "" || utf8.RuneCountInString(field) > 2000 {
			return domain.NewError(400, "skill_editorial_invalid", "名称、简介、说明与分类不能为空，且不能超过 2000 字符")
		}
	}
	if input.SortOrder < 0 || input.SortOrder > 1000000 {
		return domain.NewError(400, "skill_sort_order_invalid", "排序须为 0 至 1000000 的整数")
	}
	if !slices.Contains([]string{"draft", "listed", "hidden", "paused"}, input.Visibility) {
		return domain.NewError(400, "skill_visibility_invalid", "目录状态无效")
	}
	return nil
}

func (s *Service) ListPublicSkillEntries(ctx context.Context, subject string) ([]domain.SkillCatalogEntry, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, domain.NewError(401, "unauthorized", "未建立 Login 会话")
	}
	items, err := s.repository.ListSkillEntries(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]domain.SkillCatalogEntry, 0, len(items))
	for _, item := range items {
		if item.Visibility == "listed" {
			visible = append(visible, item)
		}
	}
	return visible, nil
}

func (s *Service) PublicSkillEntry(ctx context.Context, subject, id string) (domain.SkillCatalogEntry, error) {
	if strings.TrimSpace(subject) == "" {
		return domain.SkillCatalogEntry{}, domain.NewError(401, "unauthorized", "未建立 Login 会话")
	}
	entry, err := s.repository.SkillEntry(ctx, id)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	if entry.Visibility != "listed" {
		return domain.SkillCatalogEntry{}, domain.NewError(404, "skill_not_found", "技能目录条目不存在")
	}
	return entry, nil
}

func (s *Service) ListManagedSkillEntries(ctx context.Context, subject string) ([]domain.SkillCatalogEntry, error) {
	if err := s.requireOperationsRole(ctx, subject, "skill_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListSkillEntries(ctx)
}

func (s *Service) ManagedSkillEntry(ctx context.Context, subject, id string) (domain.SkillCatalogEntry, error) {
	if err := s.requireOperationsRole(ctx, subject, "skill_ops_admin"); err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	return s.repository.SkillEntry(ctx, id)
}

// requireSkillCatalogWriter returns the caller's active organization when it
// may change catalog records: skill_ops_admin in an organization that is not frozen.
func (s *Service) requireSkillCatalogWriter(ctx context.Context, subject string) (string, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return "", err
	}
	if !hasAnyRole(roles, "skill_ops_admin") {
		return "", domain.NewError(403, "operations_forbidden", "当前角色不能执行此运营操作")
	}
	if organization.Status == "冻结" {
		return "", domain.NewError(403, "organization_frozen", "当前组织已冻结，不能修改目录资料")
	}
	return organization.OrganizationID, nil
}

// CreateSkillEntryInput is the first step of a new skill record, as "新建应用资料"
// is for apps. Only single skills are created here; bundle composition is not
// editable yet. New records start as drafts and carry no package.
type CreateSkillEntryInput struct {
	EntryID     string `json:"entryId"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Category    string `json:"category"`
	SortOrder   int    `json:"sortOrder"`
}

var skillEntryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func (s *Service) CreateManagedSkillEntry(ctx context.Context, subject string, input CreateSkillEntryInput) (domain.SkillCatalogEntry, error) {
	organizationID, err := s.requireSkillCatalogWriter(ctx, subject)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	id := strings.TrimSpace(input.EntryID)
	if !skillEntryIDPattern.MatchString(id) {
		return domain.SkillCatalogEntry{}, domain.NewError(400, "skill_id_invalid", "技能 ID 须为 2–63 位小写字母、数字或连字符，并与技能包目录名一致")
	}
	fields := PatchSkillEntryInput{Name: input.Name, Summary: input.Summary, Description: input.Description, Category: input.Category, SortOrder: input.SortOrder, Visibility: "draft"}
	if err := validateSkillEntryInput(fields); err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	return s.repository.CreateSkillEntry(ctx, domain.SkillCatalogEntry{EntryID: id, Kind: "skill",
		Name: strings.TrimSpace(fields.Name), Summary: strings.TrimSpace(fields.Summary), Description: strings.TrimSpace(fields.Description),
		Category: strings.TrimSpace(fields.Category), SortOrder: fields.SortOrder, Visibility: "draft"}, subject, organizationID)
}

func (s *Service) PatchManagedSkillEntry(ctx context.Context, subject, id, ifMatch string, input PatchSkillEntryInput) (domain.SkillCatalogEntry, error) {
	organizationID, err := s.requireSkillCatalogWriter(ctx, subject)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	if strings.TrimSpace(ifMatch) == "" {
		return domain.SkillCatalogEntry{}, domain.NewError(428, "skill_revision_required", "请提供当前目录修订")
	}
	prefix := fmt.Sprintf(`"skill:%s:`, id)
	if !strings.HasPrefix(ifMatch, prefix) || !strings.HasSuffix(ifMatch, `"`) {
		return domain.SkillCatalogEntry{}, domain.NewError(400, "skill_revision_invalid", "目录修订格式无效")
	}
	revision, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ifMatch, prefix), `"`))
	if err != nil || revision < 1 {
		return domain.SkillCatalogEntry{}, domain.NewError(400, "skill_revision_invalid", "目录修订格式无效")
	}
	if err := validateSkillEntryInput(input); err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	current, err := s.repository.SkillEntry(ctx, id)
	if err != nil {
		return domain.SkillCatalogEntry{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Summary = strings.TrimSpace(input.Summary)
	current.Description = strings.TrimSpace(input.Description)
	current.Category = strings.TrimSpace(input.Category)
	current.SortOrder = input.SortOrder
	current.Visibility = input.Visibility
	return s.repository.UpdateSkillEntryCAS(ctx, id, revision, current, subject, organizationID)
}

func (s *Service) SkillAudit(ctx context.Context, subject, id string) ([]domain.SkillCatalogAuditEvent, error) {
	if err := s.requireOperationsRole(ctx, subject, "skill_ops_admin"); err != nil {
		return nil, err
	}
	items, err := s.repository.ListSkillAudit(ctx, id)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}
