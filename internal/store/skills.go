package store

import (
	"context"
	"sort"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// Editorial previews are not install dependency locks. B04's full execution
// dependencies remain owned by a later verified skill release manifest.
var previewBundleSkills = map[string][]string{
	"B01": {"verdantflare-image", "verdantflare-project", "verdantflare-artifact"},
	"B02": {"verdantflare-video", "verdantflare-project", "verdantflare-artifact"},
	"B03": {"verdantflare-music", "verdantflare-speech", "verdantflare-project", "verdantflare-artifact"},
	"B04": {"x-verdantflare-short-drama"},
	"B05": {"verdantflare-project", "verdantflare-artifact", "verdantflare-reference-learning"},
}

func withSkillPreview(entry domain.SkillCatalogEntry) domain.SkillCatalogEntry {
	if ids := previewBundleSkills[entry.EntryID]; len(ids) > 0 {
		entry.PreviewSkillIDs = append([]string(nil), ids...)
	}
	return entry
}

// These are editorial starting records, not release manifests or install dependencies.
func defaultSkillEntries() map[string]domain.SkillCatalogEntry {
	items := []domain.SkillCatalogEntry{
		{EntryID: "B01", Kind: "bundle", Name: "图像与角色", Summary: "从角色参考、图像生成到项目成果保存。", Category: "图像创作"},
		{EntryID: "B02", Kind: "bundle", Name: "视频创作", Summary: "规划、生成和管理视频成果。", Category: "视频创作"},
		{EntryID: "B03", Kind: "bundle", Name: "音乐与配音", Summary: "创作音乐与对白，保留可追溯版本。", Category: "声音创作"},
		{EntryID: "B04", Kind: "bundle", Name: "短剧制作", Summary: "从剧本和分镜到分批制作与返修。", Category: "短剧制作"},
		{EntryID: "B05", Kind: "bundle", Name: "项目、资产与参考学习", Summary: "沉淀素材、项目和参考方法。", Category: "项目协作"},
		{EntryID: "verdantflare-artifact", Kind: "skill", Name: "Artifact", Summary: "上传、下载与核验不可变内容版本。", Category: "项目协作"},
		{EntryID: "verdantflare-blender", Kind: "skill", Name: "Blender", Summary: "通过 Studio MCP 检查、编辑和保存 Blender 场景。", Category: "视觉创作"},
		{EntryID: "verdantflare-character-face", Kind: "skill", Name: "角色锁脸", Summary: "建立角色多角度身份参考并处理局部修复。", Category: "角色制作"},
		{EntryID: "verdantflare-image", Kind: "skill", Name: "Image", Summary: "按已注册能力生成或编辑图像候选。", Category: "图像创作"},
		{EntryID: "verdantflare-music", Kind: "skill", Name: "Music", Summary: "创作歌曲、器乐、母带及局部重绘。", Category: "声音创作"},
		{EntryID: "verdantflare-music-mv", Kind: "skill", Name: "Music MV", Summary: "基于已批准歌曲制作高潮 MV。", Category: "视频创作"},
		{EntryID: "verdantflare-project", Kind: "skill", Name: "Project", Summary: "创建、保存并恢复 Studio 项目和成果关联。", Category: "项目协作"},
		{EntryID: "verdantflare-reference-learning", Kind: "skill", Name: "Reference Learning", Summary: "拆解参考作品并复用分析成果。", Category: "项目协作"},
		{EntryID: "verdantflare-speech", Kind: "skill", Name: "Speech", Summary: "生成对白、旁白，支持听审与逐句返修。", Category: "声音创作"},
		{EntryID: "verdantflare-video", Kind: "skill", Name: "Video", Summary: "发现视频渠道，生成、恢复并下载视频成果。", Category: "视频创作"},
		{EntryID: "x-verdantflare-short-drama", Kind: "skill", Name: "VerdantFlare 短剧", Summary: "剧本、导演分镜、执行方案与已有项目续作。", Category: "短剧制作"},
	}
	result := make(map[string]domain.SkillCatalogEntry, len(items))
	for i, item := range items {
		item.SortOrder = (i + 1) * 10
		item.Visibility = "draft"
		item.Sample = true
		item.Revision = 1
		item.Distribution = "unavailable"
		item.Description = item.Summary
		result[item.EntryID] = withSkillPreview(item)
	}
	return result
}

func (m *Memory) ListSkillEntries(_ context.Context) ([]domain.SkillCatalogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.SkillCatalogEntry, 0, len(m.skillEntries))
	for _, entry := range m.skillEntries {
		result = append(result, withSkillPreview(entry))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortOrder == result[j].SortOrder {
			return result[i].EntryID < result[j].EntryID
		}
		return result[i].SortOrder < result[j].SortOrder
	})
	return result, nil
}

func (m *Memory) SkillEntry(_ context.Context, id string) (domain.SkillCatalogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.skillEntries[id]
	if !ok {
		return domain.SkillCatalogEntry{}, domain.NewError(404, "skill_not_found", "技能目录条目不存在")
	}
	return withSkillPreview(entry), nil
}

func (m *Memory) CreateSkillEntry(_ context.Context, entry domain.SkillCatalogEntry, actor, organizationID string) (domain.SkillCatalogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skillEntries == nil {
		m.skillEntries = make(map[string]domain.SkillCatalogEntry)
	}
	if _, ok := m.skillEntries[entry.EntryID]; ok {
		return domain.SkillCatalogEntry{}, domain.NewError(409, "skill_exists", "技能目录条目已存在")
	}
	entry.Revision, entry.Distribution, entry.UpdatedAt = 1, "unavailable", time.Now().UTC()
	m.skillEntries[entry.EntryID] = entry
	m.skillAudit = append(m.skillAudit, domain.SkillCatalogAuditEvent{EntryID: entry.EntryID, Action: "create", ActorSubject: actor, OrganizationID: organizationID, Revision: entry.Revision, CreatedAt: entry.UpdatedAt})
	return entry, nil
}

func (m *Memory) UpdateSkillEntryCAS(_ context.Context, id string, revision int, entry domain.SkillCatalogEntry, actor, organizationID string) (domain.SkillCatalogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.skillEntries[id]
	if !ok {
		return domain.SkillCatalogEntry{}, domain.NewError(404, "skill_not_found", "技能目录条目不存在")
	}
	if current.Revision != revision {
		return domain.SkillCatalogEntry{}, domain.NewError(409, "skill_revision_changed", "目录资料已变化，请重新回读")
	}
	entry.EntryID, entry.Kind = id, current.Kind
	entry.Sample = current.Sample
	entry.Revision, entry.Distribution, entry.UpdatedAt = revision+1, "unavailable", time.Now().UTC()
	m.skillEntries[id] = entry
	m.skillAudit = append(m.skillAudit, domain.SkillCatalogAuditEvent{EntryID: id, Action: "update", ActorSubject: actor, OrganizationID: organizationID, Revision: entry.Revision, CreatedAt: entry.UpdatedAt})
	return entry, nil
}

func (m *Memory) ListSkillAudit(_ context.Context, id string) ([]domain.SkillCatalogAuditEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.SkillCatalogAuditEvent, 0)
	for _, event := range m.skillAudit {
		if event.EntryID == id {
			result = append(result, event)
		}
	}
	return result, nil
}
