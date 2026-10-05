package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type Memory struct {
	mu sync.RWMutex

	users         map[string]domain.CenterUser
	organizations map[string]domain.Organization
	apps          map[string]domain.App
	appVersions   map[string]map[string]domain.AppVersion
	entitlements  map[string]map[string]bool
	sessions      map[string]domain.ExperienceSession
	modelRuns     map[string]domain.ModelExperienceRun
	apiKeys       map[string]domain.APIKey
	models        []domain.Model
	publicModels  map[string]domain.PublicModel
	tasks         []domain.APITask
	members       map[string][]domain.Member
	billing       map[string]domain.BillingSummary
	releases      []domain.Release
	opsOrgs       []domain.OperationsOrganization
}

// NewMemoryBootstrap creates only the operational identity needed to provision
// real organizations. Catalog, metering, billing and experience data start empty.
func NewMemoryBootstrap() *Memory {
	const organizationID = "org_verdantflare"
	const adminSubject = "00000000-0000-4000-8000-000000000001"
	return &Memory{
		users: map[string]domain.CenterUser{
			adminSubject: {
				LoginSubject: adminSubject, CenterUserID: "cu_01HUB7C9Q", DisplayName: "管理员",
				Email: "admin@verdantflarehub.com", ActiveOrganizationID: organizationID,
				Memberships: []domain.Membership{{OrganizationID: organizationID, Roles: []string{"organization_admin", "app_ops_admin", "customer_success_admin", "api_ops_admin"}}},
			},
		},
		organizations: map[string]domain.Organization{organizationID: {OrganizationID: organizationID, Name: "VerdantFlare", ShortName: "VF", EntitlementVersion: 1, Status: "正常"}},
		apps:          map[string]domain.App{}, appVersions: map[string]map[string]domain.AppVersion{}, entitlements: map[string]map[string]bool{organizationID: {}},
		sessions: map[string]domain.ExperienceSession{}, apiKeys: map[string]domain.APIKey{},
		modelRuns: map[string]domain.ModelExperienceRun{},
		models:    []domain.Model{}, publicModels: map[string]domain.PublicModel{}, tasks: []domain.APITask{}, members: map[string][]domain.Member{},
		billing: map[string]domain.BillingSummary{}, releases: []domain.Release{}, opsOrgs: []domain.OperationsOrganization{},
	}
}

func NewMemorySeeded(now time.Time) *Memory {
	verdantflare := domain.Organization{
		OrganizationID: "org_verdantflare", Name: "VerdantFlare 创作团队", ShortName: "VF",
		Plan: "Enterprise", EntitlementVersion: 12, ExperienceCredits: 680, APICredits: 12840,
		DefaultRegion: "cn-east-1", Industry: "媒体与内容制作", BillingEmail: "finance@verdantflare.com", Status: "正常",
	}
	northshore := domain.Organization{
		OrganizationID: "org_northshore", Name: "北岸视觉工作室", ShortName: "北",
		Plan: "Studio", EntitlementVersion: 7, ExperienceCredits: 160, APICredits: 3200,
		DefaultRegion: "cn-east-1", Industry: "媒体与内容制作", BillingEmail: "finance@northshore.example", Status: "正常",
	}

	apps := []domain.App{
		{ID: "comfyui-studio", Name: "ComfyUI Studio", Category: "视觉创作", Summary: "面向节点工作流的图像与视频生成环境。", Version: "1.4.2", Channel: "Stable", Status: "可用", Tone: "mint", Icon: "nodes", GPU: "L40S · 24 GB", Duration: "最长 60 分钟"},
		{ID: "wan-video", Name: "Wan Video Studio", Category: "视频生成", Summary: "从文本、图片与参考动作生成可控视频。", Version: "0.9.8", Channel: "Preview", Status: "可体验", Tone: "blue", Icon: "video", GPU: "H100 · 40 GB", Duration: "最长 30 分钟"},
		{ID: "f5-tts", Name: "F5-TTS Studio", Category: "声音与配音", Summary: "面向配音、音色实验和批量语音合成。", Version: "0.7.4", Channel: "Preview", Status: "申请体验", Tone: "amber", Icon: "wave", GPU: "L40S · 16 GB", Duration: "最长 45 分钟"},
		{ID: "open-cut", Name: "OpenCut Web", Category: "视频剪辑", Summary: "轻量浏览器剪辑、字幕与素材编排工作台。", Version: "0.9.4", Channel: "Preview", Status: "可用", Tone: "coral", Icon: "cut", GPU: "CPU · 8 Core", Duration: "最长 90 分钟"},
		{ID: "open-webui", Name: "Open WebUI", Category: "AI 助手", Summary: "连接私有模型、知识库与团队提示词。", Version: "1.0.16", Channel: "Stable", Status: "可用", Tone: "violet", Icon: "chat", GPU: "CPU · 4 Core", Duration: "最长 120 分钟"},
		{ID: "face-lip", Name: "Face & Lip Workflow", Category: "数字人", Summary: "口型同步、人物一致性与镜头批处理。", Version: "1.3.0", Channel: "Stable", Status: "可用", Tone: "mint", Icon: "face", GPU: "L40S · 24 GB", Duration: "最长 45 分钟"},
	}
	appMap := make(map[string]domain.App, len(apps))
	allEntitlements := make(map[string]bool, len(apps))
	for _, app := range apps {
		appMap[app.ID] = app
		allEntitlements[app.ID] = true
	}

	return &Memory{
		users: map[string]domain.CenterUser{
			"00000000-0000-4000-8000-000000000001": {
				CenterUserID: "cu_01HUB7C9Q", LoginSubject: "00000000-0000-4000-8000-000000000001", DisplayName: "默认管理员", Email: "admin@verdantflarehub.com", ActiveOrganizationID: verdantflare.OrganizationID,
				Memberships: []domain.Membership{
					{OrganizationID: verdantflare.OrganizationID, Roles: []string{"organization_admin", "app_ops_admin", "customer_success_admin", "api_ops_admin"}},
					{OrganizationID: northshore.OrganizationID, Roles: []string{"organization_admin"}},
				},
			},
			"logto_01vf9k2": {
				CenterUserID: "cu_01HUB7C9Q", LoginSubject: "logto_01vf9k2", DisplayName: "赵涛", Email: "zhaotao@verdantflare.com", ActiveOrganizationID: verdantflare.OrganizationID,
				Memberships: []domain.Membership{
					{OrganizationID: verdantflare.OrganizationID, Roles: []string{"organization_admin", "app_ops_admin", "customer_success_admin", "api_ops_admin"}},
					{OrganizationID: northshore.OrganizationID, Roles: []string{"organization_admin"}},
				},
			},
		},
		organizations: map[string]domain.Organization{verdantflare.OrganizationID: verdantflare, northshore.OrganizationID: northshore},
		apps:          appMap,
		entitlements: map[string]map[string]bool{
			verdantflare.OrganizationID: allEntitlements,
			northshore.OrganizationID:   {"comfyui-studio": true, "wan-video": true, "open-cut": true},
		},
		sessions: map[string]domain.ExperienceSession{
			"exp_2F7A19": {ID: "exp_2F7A19", OrganizationID: verdantflare.OrganizationID, CenterUserID: "cu_01HUB7C9Q", AppID: "comfyui-studio", App: "ComfyUI Studio", Region: "cn-east-1", StartedAt: now.Add(-18 * time.Minute), ExpiresAt: now.Add(42 * time.Minute), Remaining: "42 分钟", Status: "运行中", Usage: "18 点", CleanupStatus: "保留中"},
		},
		modelRuns: map[string]domain.ModelExperienceRun{},
		apiKeys: map[string]domain.APIKey{
			"key_prod_31": {ID: "key_prod_31", OrganizationID: verdantflare.OrganizationID, Name: "内容生产服务", Prefix: "vf_live_7p3a••••••••2k9x", Scopes: []string{"models:read", "tasks:write"}, CreatedAt: time.Date(2026, 7, 2, 8, 0, 0, 0, time.UTC), Status: "有效", SecretHash: sha256.Sum256([]byte("seed-key-prod-not-a-secret"))},
			"key_dev_18":  {ID: "key_dev_18", OrganizationID: verdantflare.OrganizationID, Name: "研发 Playground", Prefix: "vf_test_2m1c••••••••8az4", Scopes: []string{"models:read", "tasks:write", "usage:read"}, CreatedAt: time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC), Status: "有效", SecretHash: sha256.Sum256([]byte("seed-key-dev-not-a-secret"))},
		},
		models: []domain.Model{
			{ID: "verdantflare-sd2", Name: "VerdantFlare SD2", Provider: "VerdantFlare", Type: "视频生成", Context: "图文 / 动作", Latency: "异步 · 2–6 分钟", Price: "26.8 点 / 秒", Status: "可调用"},
			{ID: "deepseek-v4-pro", Name: "DeepSeek-V4 Pro", Provider: "深度求索", Type: "推理模型", Context: "1,000k", Latency: "1.8 秒", Price: "12 / 24 点 · M tokens", Status: "可调用"},
			{ID: "glm-5-2", Name: "GLM-5.2", Provider: "智谱", Type: "文本生成", Context: "1,000k", Latency: "1.4 秒", Price: "11.2 / 39.2 点 · M tokens", Status: "可调用"},
			{ID: "kimi-k2-6", Name: "Kimi-K2.6", Provider: "月之暗面", Type: "多模态", Context: "512k", Latency: "2.1 秒", Price: "6.5 / 27 点 · M tokens", Status: "可调用"},
		},
		tasks: []domain.APITask{
			{ID: "task_9D2A", OrganizationID: verdantflare.OrganizationID, Model: "VerdantFlare SD2", Created: "16:28:04", Duration: "3m 24s", Usage: "134 点", Status: "运行中"},
			{ID: "task_4CB8", OrganizationID: verdantflare.OrganizationID, Model: "GLM-5.2", Created: "16:21:17", Duration: "2.4s", Usage: "0.82 点", Status: "成功"},
		},
		members: map[string][]domain.Member{
			verdantflare.OrganizationID: {
				{Name: "赵涛", Email: "zhaotao@verdantflare.com", Role: "组织管理员", Joined: "2026-04-12", Status: "正常", Avatar: "赵"},
				{Name: "林墨", Email: "linmo@verdantflare.com", Role: "开发者", Joined: "2026-05-03", Status: "正常", Avatar: "林"},
				{Name: "陈瑜", Email: "chenyu@verdantflare.com", Role: "财务查看者", Joined: "2026-06-22", Status: "正常", Avatar: "陈"},
			},
			northshore.OrganizationID: {{Name: "北岸管理员", Email: "admin@northshore.example", Role: "组织管理员", Joined: "2026-05-20", Status: "正常", Avatar: "北"}},
		},
		billing: map[string]domain.BillingSummary{
			verdantflare.OrganizationID: {Plan: "Enterprise", CycleStart: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), CycleEnd: time.Date(now.Year(), now.Month()+1, 0, 23, 59, 59, 0, time.UTC), APIBudget: 41000, APIUsed: 28160, ExperienceUsed: 142, MemberLimit: 18, Invoices: []domain.Invoice{{Period: "2026 年 6 月", ID: "VF-202606-0138", Amount: "¥ 32,800.00", Status: "成功"}, {Period: "2026 年 5 月", ID: "VF-202605-0122", Amount: "¥ 29,460.00", Status: "成功"}}},
			northshore.OrganizationID:   {Plan: "Studio", CycleStart: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), CycleEnd: time.Date(now.Year(), now.Month()+1, 0, 23, 59, 59, 0, time.UTC), APIBudget: 8000, APIUsed: 4800, ExperienceUsed: 64, MemberLimit: 8, Invoices: []domain.Invoice{}},
		},
		releases: []domain.Release{
			{App: "ComfyUI Studio", Version: "1.4.2", Channel: "Stable", Validation: "6 / 6", Audience: "12 个组织", Updated: "今天 15:42", Status: "已发布"},
			{App: "OpenCut Web", Version: "0.9.4", Channel: "Preview", Validation: "4 / 6", Audience: "3 个组织", Updated: "今天 11:08", Status: "灰度中"},
			{App: "F5-TTS Studio", Version: "0.7.4", Channel: "Experimental", Validation: "2 / 6", Audience: "内部", Updated: "昨天", Status: "许可复核"},
			{App: "Wan Video Studio", Version: "0.9.8", Channel: "Candidate", Validation: "1 / 6", Audience: "未发放", Updated: "7 月 15 日", Status: "适配中"},
		},
		opsOrgs: []domain.OperationsOrganization{
			{ID: "org_verdantflare", Name: "VerdantFlare 创作团队", Plan: "Enterprise", Members: 18, Apps: 22, APIUsage: "¥ 32,800", Expires: "2027-03-31", Status: "正常"},
			{ID: "org_northshore", Name: "北岸视觉工作室", Plan: "Studio", Members: 8, Apps: 12, APIUsage: "¥ 8,460", Expires: "2026-12-31", Status: "正常"},
			{ID: "org_haiyu", Name: "海屿制作团队", Plan: "Pilot", Members: 4, Apps: 8, APIUsage: "¥ 1,820", Expires: "剩余 12 天", Status: "试用中"},
		},
	}
}

func (m *Memory) CenterContext(_ context.Context, subject string) (domain.CenterContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.centerContextLocked(subject)
}

func (m *Memory) centerContextLocked(subject string) (domain.CenterContext, error) {
	user, ok := m.users[subject]
	if !ok {
		return domain.CenterContext{}, domain.NewError(403, "center_user_not_found", "登录主体尚未绑定 Center User")
	}
	organizations := make([]domain.Organization, 0, len(user.Memberships))
	for _, membership := range user.Memberships {
		if membership.Status == "停用" {
			continue
		}
		organization, exists := m.organizations[membership.OrganizationID]
		if !exists {
			continue
		}
		organization.Roles = append([]string(nil), membership.Roles...)
		organizations = append(organizations, organization)
	}
	activeID := user.ActiveOrganizationID
	if membership, ok := membershipFor(user, activeID); !ok || membership.Status == "停用" {
		activeID = ""
		if len(organizations) > 0 {
			activeID = organizations[0].OrganizationID
		}
	}
	return domain.CenterContext{CenterUserID: user.CenterUserID, LoginSubject: user.LoginSubject, DisplayName: user.DisplayName, Email: user.Email, Organizations: organizations, ActiveOrganizationID: activeID}, nil
}

func (m *Memory) SetActiveOrganization(_ context.Context, subject, organizationID string) (domain.CenterContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[subject]
	if !ok {
		return domain.CenterContext{}, domain.NewError(403, "center_user_not_found", "登录主体尚未绑定 Center User")
	}
	if membership, ok := membershipFor(user, organizationID); !ok || membership.Status == "停用" {
		return domain.CenterContext{}, domain.NewError(403, "organization_forbidden", "不能访问该组织")
	}
	user.ActiveOrganizationID = organizationID
	m.users[subject] = user
	return m.centerContextLocked(subject)
}

func (m *Memory) ActiveOrganization(_ context.Context, subject string) (domain.Organization, []string, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[subject]
	if !ok {
		return domain.Organization{}, nil, "", domain.NewError(403, "center_user_not_found", "登录主体尚未绑定 Center User")
	}
	activeID := user.ActiveOrganizationID
	membership, ok := membershipFor(user, activeID)
	if !ok || membership.Status == "停用" {
		for _, candidate := range user.Memberships {
			if candidate.Status != "停用" {
				activeID, membership, ok = candidate.OrganizationID, candidate, true
				break
			}
		}
	}
	if !ok || membership.Status == "停用" {
		return domain.Organization{}, nil, "", domain.NewError(403, "organization_forbidden", "当前组织不可访问")
	}
	organization, ok := m.organizations[activeID]
	if !ok {
		return domain.Organization{}, nil, "", domain.NewError(404, "organization_not_found", "组织不存在")
	}
	return organization, append([]string(nil), membership.Roles...), user.CenterUserID, nil
}

func (m *Memory) Organization(_ context.Context, organizationID string) (domain.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	organization, ok := m.organizations[organizationID]
	if !ok {
		return domain.Organization{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	return organization, nil
}

func (m *Memory) UpdateOrganization(_ context.Context, organizationID string, update domain.Organization) (domain.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	organization, ok := m.organizations[organizationID]
	if !ok {
		return domain.Organization{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	if update.Name != "" {
		organization.Name = update.Name
	}
	if update.ShortName != "" {
		organization.ShortName = update.ShortName
	}
	if update.DefaultRegion != "" {
		organization.DefaultRegion = update.DefaultRegion
	}
	if update.Industry != "" {
		organization.Industry = update.Industry
	}
	if update.BillingEmail != "" {
		organization.BillingEmail = update.BillingEmail
	}
	m.organizations[organizationID] = organization
	return organization, nil
}

func (m *Memory) ListApps(_ context.Context, organizationID string) ([]domain.App, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entitlements := m.entitlements[organizationID]
	active := m.organizations[organizationID].Status != "冻结"
	result := make([]domain.App, 0, len(m.apps))
	for id, app := range m.apps {
		if !published(app) {
			continue
		}
		app.Entitled = active && entitlements[id]
		if app.PublicVisible || app.Entitled {
			result = append(result, app)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (m *Memory) GetApp(_ context.Context, organizationID, appID string) (domain.App, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	app, ok := m.apps[appID]
	if !ok || !published(app) {
		return domain.App{}, domain.NewError(404, "app_not_found", "应用不存在")
	}
	app.Entitled = m.organizations[organizationID].Status != "冻结" && m.entitlements[organizationID][appID]
	if !app.PublicVisible && !app.Entitled {
		return domain.App{}, domain.NewError(404, "app_not_found", "应用不存在或当前组织无权查看")
	}
	return app, nil
}

func (m *Memory) ListExperienceSessions(_ context.Context, organizationID string) ([]domain.ExperienceSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.ExperienceSession, 0)
	for _, session := range m.sessions {
		if session.OrganizationID == organizationID {
			result = append(result, session)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.After(result[j].StartedAt) })
	return result, nil
}

func (m *Memory) CreateExperienceSession(_ context.Context, organizationID, centerUserID string, app domain.App, region string, duration time.Duration, reservedCredits int) (domain.ExperienceSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	organization, ok := m.organizations[organizationID]
	if !ok {
		return domain.ExperienceSession{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	active := 0
	for _, session := range m.sessions {
		if session.OrganizationID == organizationID && session.Status == "运行中" {
			active++
		}
	}
	if active >= 2 {
		return domain.ExperienceSession{}, domain.NewError(409, "experience_concurrency_exceeded", "当前组织已达到体验 Session 并发上限")
	}
	if organization.ExperienceCredits < reservedCredits {
		return domain.ExperienceSession{}, domain.NewError(402, "experience_credits_insufficient", "体验额度不足")
	}
	now := time.Now().UTC()
	session := domain.ExperienceSession{
		ID: newID("exp"), OrganizationID: organizationID, CenterUserID: centerUserID,
		AppID: app.ID, App: app.Name, Region: region, StartedAt: now, ExpiresAt: now.Add(duration),
		Remaining: fmt.Sprintf("%d 分钟", int(duration.Minutes())), Status: "运行中", Usage: "0 点", CleanupStatus: "保留中",
	}
	organization.ExperienceCredits -= reservedCredits
	m.organizations[organizationID] = organization
	m.sessions[session.ID] = session
	return session, nil
}

func (m *Memory) CloseExperienceSession(_ context.Context, organizationID, sessionID string) (domain.ExperienceSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok || session.OrganizationID != organizationID {
		return domain.ExperienceSession{}, domain.NewError(404, "experience_session_not_found", "体验 Session 不存在")
	}
	if session.Status != "运行中" {
		return session, nil
	}
	now := time.Now().UTC()
	session.Status = "已结束"
	session.Remaining = "用户关闭"
	session.ClosedAt = &now
	session.CleanupStatus = "清理中"
	m.sessions[sessionID] = session
	return session, nil
}

func (m *Memory) ListAPIKeys(_ context.Context, organizationID string) ([]domain.APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.APIKey, 0)
	for _, key := range m.apiKeys {
		if key.OrganizationID == organizationID {
			if key.Status == "有效" && key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now().UTC()) {
				key.Status = "已过期"
			}
			result = append(result, key)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) CreateAPIKey(_ context.Context, organizationID string, key domain.APIKey) (domain.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key.OrganizationID = organizationID
	m.apiKeys[key.ID] = key
	return key, nil
}

func (m *Memory) RevokeAPIKey(_ context.Context, organizationID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.apiKeys[keyID]
	if !ok || key.OrganizationID != organizationID {
		return domain.NewError(404, "api_key_not_found", "API Key 不存在")
	}
	key.Status = "已撤销"
	m.apiKeys[keyID] = key
	return nil
}

func (m *Memory) ListModels(_ context.Context, _ string) ([]domain.Model, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]domain.Model{}, m.models...), nil
}

func (m *Memory) ListAPITasks(_ context.Context, organizationID string) ([]domain.APITask, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.APITask, 0)
	for _, task := range m.tasks {
		if task.OrganizationID == organizationID {
			result = append(result, task)
		}
	}
	return result, nil
}

func (m *Memory) Usage(_ context.Context, organizationID string) (domain.UsageSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	billing, ok := m.billing[organizationID]
	if !ok {
		return domain.UsageSummary{}, domain.NewError(404, "usage_not_found", "暂无用量数据")
	}
	percentage := 0.0
	if billing.APIBudget > 0 {
		percentage = float64(billing.APIUsed) / float64(billing.APIBudget) * 100
	}
	return domain.UsageSummary{Budget: float64(billing.APIBudget), Used: float64(billing.APIUsed), Remaining: float64(billing.APIBudget - billing.APIUsed), Percentage: percentage, ByModel: map[string]int{}}, nil
}

func (m *Memory) ListMembers(_ context.Context, organizationID string) ([]domain.Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.organizations[organizationID]; !ok {
		return nil, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	return m.membersForLocked(organizationID), nil
}

func (m *Memory) AddMember(_ context.Context, organizationID string, member domain.Member) (domain.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.organizations[organizationID]; !ok {
		return domain.Member{}, domain.NewError(404, "organization_not_found", "组织不存在")
	}
	for _, existing := range m.members[organizationID] {
		if strings.EqualFold(existing.Email, member.Email) && existing.Status != "已取消" {
			return domain.Member{}, domain.NewError(409, "member_exists", "该邮箱已在组织中或已有待处理邀请")
		}
	}
	for _, user := range m.users {
		if strings.EqualFold(user.Email, member.Email) {
			if _, ok := membershipFor(user, organizationID); ok {
				return domain.Member{}, domain.NewError(409, "member_exists", "该用户已经属于该组织")
			}
		}
	}
	if member.ID == "" {
		member.ID = newID("mem")
	}
	m.members[organizationID] = append(m.members[organizationID], member)
	return member, nil
}

func (m *Memory) Billing(_ context.Context, organizationID string) (domain.BillingSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	billing, ok := m.billing[organizationID]
	if !ok {
		return domain.BillingSummary{}, domain.NewError(404, "billing_not_found", "暂无账单数据")
	}
	return billing, nil
}

func (m *Memory) ListReleases(_ context.Context) ([]domain.Release, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.Release, 0, len(m.apps))
	for _, app := range m.apps {
		result = append(result, m.releaseForLocked(app))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].App < result[j].App })
	return result, nil
}

func (m *Memory) ListOperationsOrganizations(_ context.Context) ([]domain.OperationsOrganization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.OperationsOrganization, 0, len(m.organizations))
	for id, organization := range m.organizations {
		appCount := 0
		for appID, enabled := range m.entitlements[id] {
			if enabled && published(m.apps[appID]) {
				appCount++
			}
		}
		result = append(result, domain.OperationsOrganization{ID: id, Name: organization.Name, Plan: organization.Plan, Members: len(m.membersForLocked(id)), Apps: appCount, APIUsage: "未接入", Expires: "—", Status: organization.Status})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func membershipFor(user domain.CenterUser, organizationID string) (domain.Membership, bool) {
	for _, membership := range user.Memberships {
		if membership.OrganizationID == organizationID {
			return membership, true
		}
	}
	return domain.Membership{}, false
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%X", prefix, time.Now().UnixNano())
}
