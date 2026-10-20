package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"gopkg.in/yaml.v3"
)

type CreateAppV2Input struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Summary     string `json:"summary"`
	GroupID     string `json:"groupId"`
	Brand       string `json:"brand"`
	IconRef     string `json:"iconRef"`
	Category    string `json:"category"`
	SortOrder   *int   `json:"sortOrder"`
}

type PatchAppV2Input struct {
	Name             *string               `json:"name"`
	Description      *string               `json:"description"`
	Summary          *string               `json:"summary"`
	GroupID          *string               `json:"groupId"`
	Brand            *string               `json:"brand"`
	IconRef          *string               `json:"iconRef"`
	Category         *string               `json:"category"`
	SortOrder        *int                  `json:"sortOrder"`
	Logo             *domain.LogoRef       `json:"logo"`
	Visibility       *string               `json:"visibility"`
	Enabled          *bool                 `json:"enabled"`
	DisabledReason   *string               `json:"disabledReason"`
	Models           *[]string             `json:"models"`
	Capabilities     *[]string             `json:"capabilities"`
	Images           *[]domain.AppImageRef `json:"images"`
	Version          *string               `json:"version"`
	Channel          *string               `json:"channel"`
	Developer        *string               `json:"developer"`
	PublicIconURL    *string               `json:"publicIconUrl"`
	PublicVisible    *bool                 `json:"publicVisible"`
	Showcase         *domain.AppShowcase   `json:"showcase"`
	PublishedVersion *string               `json:"publishedVersion"`
	Revision         *int                  `json:"revision"`
	CreatedAt        *string               `json:"createdAt"`
	UpdatedAt        *string               `json:"updatedAt"`
}

type ReleaseSelectionInput struct {
	Version *string `json:"version"`
}

func validateAppRevision(app domain.App, ifMatch string) error {
	trimmed := strings.Trim(strings.TrimSpace(ifMatch), `"`)
	if trimmed == "" {
		return domain.NewError(428, "REVISION_REQUIRED", "请求必须携带 If-Match 修订头")
	}
	expected := fmt.Sprintf("app:%s:%d", app.ID, app.Revision)
	if trimmed != expected && trimmed != strconv.Itoa(app.Revision) {
		return domain.NewError(409, "APP_REVISION_CHANGED", "应用资料或发布版本已被更新，请刷新后重试")
	}
	return nil
}

func (s *Service) ListManagedAppsV2(ctx context.Context, subject string) ([]domain.App, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListManagedApps(ctx)
}

func (s *Service) CreateManagedAppV2(ctx context.Context, subject string, input CreateAppV2Input) (domain.App, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.App{}, err
	}
	appID := strings.TrimSpace(input.AppID)
	if appID == "" {
		appID = strings.TrimSpace(input.ID)
	}
	if !appIDPattern.MatchString(appID) {
		return domain.App{}, domain.NewError(400, "app_id_invalid", "应用 ID 须为 2–63 位小写字母、数字或连字符")
	}
	name := strings.TrimSpace(input.Name)
	if n := utf8.RuneCountInString(name); n < 2 || n > 128 {
		return domain.App{}, domain.NewError(400, "app_name_invalid", "应用名称须为 2–128 个字符")
	}
	groupID := strings.TrimSpace(input.GroupID)
	if !slices.Contains([]string{"system", "channel", "image", "video", "music", "desktop", "agent"}, groupID) {
		return domain.App{}, domain.NewError(400, "app_group_invalid", "groupId 须为 system、channel、image、video、music、desktop 或 agent")
	}
	sortOrder := 1000
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}
	if sortOrder < 0 || sortOrder > 1000000 {
		return domain.App{}, domain.NewError(400, "app_sort_order_invalid", "sortOrder 须为 0–1000000 的整数")
	}
	description := strings.TrimSpace(input.Description)
	summary := strings.TrimSpace(input.Summary)
	if description == "" && summary != "" {
		description = summary
	}
	if summary == "" && description != "" {
		summary = description
		if utf8.RuneCountInString(summary) > 512 {
			runes := []rune(summary)
			summary = string(runes[:512])
		}
	}
	if utf8.RuneCountInString(description) < 1 || utf8.RuneCountInString(description) > 16384 {
		return domain.App{}, domain.NewError(400, "app_description_invalid", "应用说明须为 1–16384 个字符")
	}
	if n := utf8.RuneCountInString(summary); n < 1 || n > 512 {
		return domain.App{}, domain.NewError(400, "app_summary_invalid", "一句话简介须为 1–512 个字符")
	}
	category := strings.TrimSpace(input.Category)
	if category == "" {
		category = groupID
	}
	brand := strings.TrimSpace(input.Brand)
	iconRef := strings.TrimSpace(input.IconRef)
	if iconRef == "" && brand != "" {
		iconRef = "brand:" + brand
	}
	created, err := s.repository.CreateManagedApp(ctx, domain.App{
		ID:             appID,
		AppID:          appID,
		Name:           name,
		Description:    description,
		Summary:        summary,
		GroupID:        groupID,
		Brand:          brand,
		IconRef:        iconRef,
		Category:       category,
		SortOrder:      sortOrder,
		Visibility:     "hidden",
		Enabled:        false,
		DisabledReason: "资料尚未上架",
		Channel:        "Candidate",
		Icon:           "market",
		Tone:           "mint",
	})
	if err != nil {
		return domain.App{}, err
	}
	return created.App, nil
}

func (s *Service) PatchManagedAppV2(ctx context.Context, subject, appID, ifMatch string, input PatchAppV2Input) (domain.App, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.App{}, err
	}
	if input.PublishedVersion != nil || input.Revision != nil || input.CreatedAt != nil || input.UpdatedAt != nil {
		return domain.App{}, domain.NewError(400, "immutable_field_forbidden", "PATCH 禁止直接修改 publishedVersion、revision 或时间")
	}
	current, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return domain.App{}, err
	}
	if err := validateAppRevision(current.App, ifMatch); err != nil {
		return domain.App{}, err
	}
	updated := current.App
	if input.Name != nil {
		updated.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		updated.Description = strings.TrimSpace(*input.Description)
	}
	if input.Summary != nil {
		updated.Summary = strings.TrimSpace(*input.Summary)
	}
	if input.GroupID != nil {
		newGroup := strings.TrimSpace(*input.GroupID)
		if updated.GroupID != "" && newGroup != updated.GroupID {
			return domain.App{}, domain.NewError(409, "app_group_immutable", "已建应用的分组不能改变")
		}
		updated.GroupID = newGroup
	}
	if input.Brand != nil {
		updated.Brand = strings.TrimSpace(*input.Brand)
	}
	if input.IconRef != nil {
		updated.IconRef = strings.TrimSpace(*input.IconRef)
	}
	if input.Category != nil {
		updated.Category = strings.TrimSpace(*input.Category)
	}
	if input.SortOrder != nil {
		updated.SortOrder = *input.SortOrder
	}
	if input.Logo != nil {
		logoCopy := *input.Logo
		updated.Logo = &logoCopy
	}
	if input.Visibility != nil {
		vis := strings.TrimSpace(*input.Visibility)
		if !slices.Contains([]string{"hidden", "listed"}, vis) {
			return domain.App{}, domain.NewError(400, "visibility_invalid", "visibility 须为 hidden 或 listed")
		}
		updated.Visibility = vis
		if vis == "listed" && updated.Channel == "Candidate" {
			updated.Channel = "Listed"
		} else if vis == "hidden" && updated.Channel == "Listed" {
			updated.Channel = "Candidate"
		}
	}
	if input.Channel != nil {
		ch := strings.TrimSpace(*input.Channel)
		if !slices.Contains([]string{"Candidate", "Listed", "Preview", "Stable", "Paused"}, ch) {
			return domain.App{}, domain.NewError(400, "channel_invalid", "不支持的发布通道")
		}
		updated.Channel = ch
		if input.Visibility == nil {
			if slices.Contains([]string{"Listed", "Preview", "Stable"}, ch) {
				updated.Visibility = "listed"
			} else {
				updated.Visibility = "hidden"
			}
		}
	}
	if input.Enabled != nil {
		updated.Enabled = *input.Enabled
	}
	if input.DisabledReason != nil {
		updated.DisabledReason = strings.TrimSpace(*input.DisabledReason)
	}
	if input.Models != nil {
		updated.Models = slices.Clone(*input.Models)
	}
	if input.Capabilities != nil {
		updated.Capabilities = slices.Clone(*input.Capabilities)
	}
	if input.Images != nil {
		updated.Images = slices.Clone(*input.Images)
	}
	if input.Version != nil {
		updated.Version = strings.TrimSpace(*input.Version)
	}
	if input.Developer != nil {
		updated.Developer = strings.TrimSpace(*input.Developer)
	}
	if input.PublicIconURL != nil {
		updated.PublicIconURL = strings.TrimSpace(*input.PublicIconURL)
	}
	if input.PublicVisible != nil {
		updated.PublicVisible = *input.PublicVisible
	} else if input.Visibility != nil {
		updated.PublicVisible = updated.Visibility == "listed"
	}
	if input.Showcase != nil {
		updated.Showcase = *input.Showcase
	}
	if err := validateEditorialFields(updated); err != nil {
		return domain.App{}, err
	}
	if current.App.Visibility != "listed" && updated.Visibility == "listed" {
		if readiness := publicationReadiness(updated); !readiness.Ready {
			return domain.App{}, domain.NewError(422, "LISTING_INCOMPLETE", "上架资料不完整："+strings.Join(readiness.MissingFields, ", "))
		}
	}

	if utf8.RuneCountInString(updated.Name) < 2 || utf8.RuneCountInString(updated.Name) > 128 {
		return domain.App{}, domain.NewError(400, "app_invalid", "应用名称须为 2–128 个字符")
	}
	if updated.SortOrder < 0 || updated.SortOrder > 1000000 {
		return domain.App{}, domain.NewError(400, "app_sort_order_invalid", "sortOrder 须为 0–1000000 的整数")
	}
	if updated.GroupID != "" && !slices.Contains([]string{"system", "channel", "image", "video", "music", "desktop", "agent"}, updated.GroupID) {
		return domain.App{}, domain.NewError(400, "app_group_invalid", "不支持的应用分组")
	}
	if updated.Enabled && updated.PublishedVersion == "" {
		return domain.App{}, domain.NewError(422, "NO_PUBLISHED_VERSION", "尚未选择已验证的发布版本，不能将应用设为可安装（enabled=true）")
	}
	if !updated.Enabled && strings.TrimSpace(updated.DisabledReason) == "" {
		if updated.PublishedVersion == "" {
			updated.DisabledReason = "尚未发布可安装的 Chart OCI 版本"
		} else {
			updated.DisabledReason = "当前暂停分发"
		}
	}
	if updated.Enabled {
		updated.DisabledReason = ""
	}
	res, err := s.repository.UpdateManagedAppIfRevision(ctx, appID, updated, current.App.Revision)
	if err != nil {
		return domain.App{}, err
	}
	return res.App, nil
}

func (s *Service) SelectAppReleaseV2(ctx context.Context, subject, appID, ifMatch string, version *string) (domain.App, error) {
	return s.selectAppReleaseV2(ctx, subject, appID, ifMatch, version, false)
}

func (s *Service) selectAppReleaseV2(ctx context.Context, subject, appID, ifMatch string, version *string, requireInstallGrant bool) (domain.App, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.App{}, err
	}
	current, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return domain.App{}, err
	}
	if err := validateAppRevision(current.App, ifMatch); err != nil {
		return domain.App{}, err
	}
	updated := current.App
	if version != nil && strings.TrimSpace(*version) != "" {
		if readiness := publicationReadiness(updated); !readiness.Ready {
			return domain.App{}, domain.NewError(422, "LISTING_INCOMPLETE", "发布资料不完整："+strings.Join(readiness.MissingFields, ", "))
		}
		targetVer := strings.TrimSpace(*version)
		verRecord, err := s.repository.AppVersion(ctx, appID, targetVer)
		if err != nil {
			return domain.App{}, domain.NewError(422, "VERSION_NOT_FOUND", "目标发行版本不存在或不属于该应用")
		}
		if verRecord.Chart == nil || verRecord.Chart.ManifestDigest == "" || verRecord.Chart.PackageDigest == "" {
			return domain.App{}, domain.NewError(422, "CHART_REQUIRED", "目标版本缺少已校验的 Chart OCI 制品记录")
		}
		if _, err := s.fetchRegistryManifest(ctx, appID, verRecord.Chart.Version, verRecord.Chart.ManifestDigest); err != nil {
			return domain.App{}, err
		}
		updated.PublishedVersion = verRecord.Version
		updated.Version = verRecord.Version
		updated.Enabled = true
		updated.DisabledReason = ""
		updated.Visibility = "listed"
		updated.Channel = "Stable"
		updated.PublicVisible = true
		// The publisher is self-bootstrapped from Hub before its MCP can run.
		// Hub release selection must not make this admin tool installable by all Stations.
		if releaseRequiresInstallGrant(appID, requireInstallGrant) {
			updated.InstallGrantRequired = true
		}
		if len(verRecord.Models) > 0 || updated.Models == nil {
			updated.Models = slices.Clone(verRecord.Models)
		}
		if len(verRecord.Capabilities) > 0 || updated.Capabilities == nil {
			updated.Capabilities = slices.Clone(verRecord.Capabilities)
		}
		if len(verRecord.Images) > 0 || updated.Images == nil {
			updated.Images = slices.Clone(verRecord.Images)
		}
		if verRecord.Logo != nil && updated.Logo == nil {
			logoCopy := *verRecord.Logo
			updated.Logo = &logoCopy
		}
	} else {
		updated.PublishedVersion = ""
		updated.Enabled = false
		if strings.TrimSpace(updated.DisabledReason) == "" {
			updated.DisabledReason = "尚未发布可安装的 Chart OCI 版本"
		}
		if updated.Channel == "Stable" || updated.Channel == "Preview" {
			updated.Channel = "Listed"
		}
	}
	res, err := s.repository.UpdateManagedAppIfRevision(ctx, appID, updated, current.App.Revision)
	if err != nil {
		return domain.App{}, err
	}
	return res.App, nil
}

func releaseRequiresInstallGrant(appID string, requested bool) bool {
	return requested || appID == "app-publishing"
}

func (s *Service) MarketAppsYAML(ctx context.Context, organizationID string, stationFeed bool) (domain.MarketAppsYaml, []byte, string, error) {
	apps, err := s.repository.ListManagedApps(ctx)
	if err != nil {
		return domain.MarketAppsYaml{}, nil, "", err
	}
	items := make([]domain.MarketCatalogItem, 0, len(apps))
	for _, app := range apps {
		isListed := app.Visibility == "listed" || slices.Contains([]string{"Listed", "Preview", "Stable"}, app.Channel)
		isCatalogGroup := slices.Contains([]string{"system", "channel", "image", "video", "music", "desktop"}, app.GroupID)
		if !isListed || (!isCatalogGroup && app.PublishedVersion == "") {
			continue
		}
		var pubVer *domain.AppVersion
		targetVer := strings.TrimSpace(app.PublishedVersion)
		if targetVer != "" {
			if v, err := s.repository.AppVersion(ctx, app.ID, targetVer); err == nil && v.Chart != nil {
				pubVer = &v
			}
		} else if app.Version != "" && (app.Channel == "Stable" || app.Channel == "Preview") {
			if v, err := s.repository.AppVersion(ctx, app.ID, app.Version); err == nil && v.Chart != nil {
				pubVer = &v
			}
		}

		desc := strings.TrimSpace(app.Description)
		if desc == "" {
			desc = strings.TrimSpace(app.Summary)
		}
		groupID := app.GroupID
		if groupID == "" {
			groupID = "system"
		}
		iconRef := strings.TrimSpace(app.IconRef)
		if iconRef == "" && app.Logo != nil {
			iconRef = app.Logo.URL
		}
		brand := strings.TrimSpace(app.Brand)
		if brand == "" && strings.HasPrefix(iconRef, "brand:") {
			brand = strings.TrimPrefix(iconRef, "brand:")
		}

		models := app.Models
		capabilities := app.Capabilities
		images := app.Images
		if pubVer != nil {
			if len(pubVer.Models) > 0 || models == nil {
				models = pubVer.Models
			}
			if len(pubVer.Capabilities) > 0 || capabilities == nil {
				capabilities = pubVer.Capabilities
			}
			if len(pubVer.Images) > 0 || images == nil {
				images = pubVer.Images
			}
		}
		if models == nil {
			models = []string{}
		}
		if capabilities == nil {
			capabilities = []string{}
		}
		if images == nil {
			images = []domain.AppImageRef{}
		}

		item := domain.MarketCatalogItem{
			AppID:        app.ID,
			DisplayName:  app.Name,
			SortOrder:    app.SortOrder,
			GroupID:      groupID,
			Brand:        brand,
			IconRef:      iconRef,
			Description:  desc,
			Models:       models,
			Capabilities: capabilities,
			Images:       images,
		}

		distributable := pubVer != nil && stationReleaseVisible(app) && pubVer.Version == stationCurrentVersion(app)
		// Station only receives a release that has passed chart verification and is enabled for distribution.
		if stationFeed && !distributable {
			continue
		}
		if distributable {
			item.Version = pubVer.Version
			item.Chart = pubVer.Chart.OCIRef
			item.ChartVersion = pubVer.Chart.Version
		} else {
			reason := strings.TrimSpace(app.DisabledReason)
			if reason == "" {
				if pubVer == nil {
					reason = "尚未发布可安装的 Chart OCI 版本"
				} else {
					reason = "当前暂停分发"
				}
			}
			item.DisabledReason = reason
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return items[i].AppID < items[j].AppID
	})

	if len(items) > 1000 {
		return domain.MarketAppsYaml{}, nil, "", domain.NewError(502, "CATALOG_LIMIT_EXCEEDED", "应用目录条目数超过 1000 项上限")
	}
	catalog := domain.MarketAppsYaml{
		SchemaVersion: 2,
		Apps:          items,
	}
	raw, err := yaml.Marshal(catalog)
	if err != nil {
		return domain.MarketAppsYaml{}, nil, "", domain.NewError(500, "CATALOG_ENCODE_FAILED", "生成 YAML 应用目录失败")
	}
	if len(raw) > 4<<20 {
		return domain.MarketAppsYaml{}, nil, "", domain.NewError(502, "CATALOG_LIMIT_EXCEEDED", "应用目录响应体超过 4 MiB 上限")
	}
	sum := sha256.Sum256(append([]byte(organizationID+"\n"), raw...))
	etag := fmt.Sprintf(`"catalog-v2-%s"`, hex.EncodeToString(sum[:16]))
	return catalog, raw, etag, nil
}

func (s *Service) StationAppVersionV2(ctx context.Context, appID, version string) (domain.AppVersion, error) {
	managed, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return domain.AppVersion{}, err
	}
	// Historical verified versions remain readable for already installed clients;
	// only the grant endpoint restricts new installs to the current pointer.
	if !stationReleaseVisible(managed.App) || stationCurrentVersion(managed.App) == "" {
		return domain.AppVersion{}, domain.NewError(404, "app_not_found", "应用未上架或不在可见范围")
	}
	ver, err := s.repository.AppVersion(ctx, appID, version)
	if err != nil {
		return domain.AppVersion{}, err
	}
	if ver.Chart == nil || ver.Chart.ManifestDigest == "" || ver.Chart.PackageDigest == "" {
		return domain.AppVersion{}, domain.NewError(404, "version_not_found", "版本未通过发行校验")
	}
	return ver, nil
}
