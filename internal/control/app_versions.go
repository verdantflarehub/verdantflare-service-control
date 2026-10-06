package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

var candidateVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
var candidateDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sourceRevisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var templateRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)
var workloadNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var candidateImagePattern = regexp.MustCompile(`^\S+:(?:[a-z0-9][a-z0-9._-]*-)?v?[0-9]+\.[0-9]+\.[0-9]+@sha256:[a-f0-9]{64}$`)

type CreateAppVersionInput struct {
	Version         string                 `json:"version"`
	UpstreamVersion string                 `json:"upstreamVersion"`
	Publisher       string                 `json:"publisher"`
	SourceURL       string                 `json:"sourceUrl"`
	SourceRevision  string                 `json:"sourceRevision"`
	LicenseID       string                 `json:"licenseId"`
	LicenseURL      string                 `json:"licenseUrl"`
	Manifest        json.RawMessage        `json:"manifest"`
	Dependencies    []domain.AppDependency `json:"dependencies"`
	Permissions     []domain.AppPermission `json:"permissions"`
}

type candidateManifest struct {
	AppID        string `json:"app_id"`
	Version      string `json:"version"`
	DisplayName  string `json:"display_name"`
	Capabilities []struct {
		ID      string `json:"capability_id"`
		Version string `json:"version"`
	} `json:"capabilities"`
	RequiredModels []struct {
		ID      string `json:"model_id"`
		Version string `json:"version"`
	} `json:"required_models"`
	RequiredResources struct {
		GPU       *int   `json:"gpu"`
		DiskBytes *int64 `json:"disk_bytes"`
	} `json:"required_resources"`
	GroupID    string `json:"group_id"`
	Deployment struct {
		TemplateRef  string `json:"template_ref"`
		WorkloadName string `json:"workload_name"`
		Images       []struct {
			Component string `json:"component"`
			Image     string `json:"image"`
		} `json:"images"`
	} `json:"deployment"`
	Entrypoints []struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		Port     int    `json:"port"`
		Path     string `json:"path"`
	} `json:"entrypoints,omitempty"`
}

func validAuditURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validateCandidateManifest(raw []byte, appID, version string, evidence []domain.AppDependency) (candidateManifest, []domain.AppArtifact, error) {
	var manifest candidateManifest
	if len(raw) == 0 || len(raw) > 256<<10 {
		return manifest, nil, domain.NewError(400, "manifest_invalid", "安装清单必须是 256 KiB 以内的 v1 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, nil, domain.NewError(400, "manifest_invalid", "安装清单不是有效的 v1 Manifest")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return manifest, nil, domain.NewError(400, "manifest_invalid", "安装清单只能包含一个对象")
	}
	if manifest.AppID != appID || manifest.Version != version || strings.TrimSpace(manifest.DisplayName) == "" ||
		!slices.Contains([]string{"image", "music", "video"}, manifest.GroupID) ||
		len(manifest.Capabilities) == 0 || manifest.RequiredModels == nil ||
		manifest.RequiredResources.GPU == nil || *manifest.RequiredResources.GPU < 0 ||
		manifest.RequiredResources.DiskBytes == nil || *manifest.RequiredResources.DiskBytes < 0 ||
		!templateRefPattern.MatchString(manifest.Deployment.TemplateRef) ||
		!workloadNamePattern.MatchString(manifest.Deployment.WorkloadName) || len(manifest.Deployment.WorkloadName) > 63 ||
		len(manifest.Deployment.Images) == 0 {
		return manifest, nil, domain.NewError(400, "manifest_invalid", "清单身份、能力、资源、模板或镜像声明不完整")
	}
	capabilities := make(map[string]bool)
	for _, capability := range manifest.Capabilities {
		if strings.TrimSpace(capability.ID) == "" || !candidateVersionPattern.MatchString(capability.Version) || capabilities[capability.ID+"@"+capability.Version] {
			return manifest, nil, domain.NewError(400, "manifest_invalid", "能力声明无效或重复")
		}
		capabilities[capability.ID+"@"+capability.Version] = true
	}
	modelEvidence := make(map[string]bool)
	for _, dependency := range evidence {
		if dependency.Kind == "model" {
			modelEvidence[dependency.ID+"@"+dependency.Version] = true
		}
	}
	models := make(map[string]bool)
	for _, required := range manifest.RequiredModels {
		key := required.ID + "@" + required.Version
		if strings.TrimSpace(required.ID) == "" || !candidateVersionPattern.MatchString(required.Version) || models[key] || !modelEvidence[key] {
			return manifest, nil, domain.NewError(400, "model_evidence_missing", "每个必需模型都要有同版本的来源、摘要与许可证证据")
		}
		models[key] = true
	}
	artifacts := make([]domain.AppArtifact, 0, len(manifest.Deployment.Images))
	components := make(map[string]bool)
	for _, image := range manifest.Deployment.Images {
		if strings.TrimSpace(image.Component) == "" || components[image.Component] || !candidateImagePattern.MatchString(image.Image) {
			return manifest, nil, domain.NewError(400, "image_digest_invalid", "每个组件镜像必须有唯一名称、固定版本与 sha256 摘要")
		}
		components[image.Component] = true
		artifacts = append(artifacts, domain.AppArtifact{Component: image.Component, Ref: image.Image, SHA256: image.Image[len(image.Image)-64:]})
	}
	entryNames := make(map[string]bool)
	for _, entry := range manifest.Entrypoints {
		if strings.TrimSpace(entry.Name) == "" || entryNames[entry.Name] ||
			!slices.Contains([]string{"http", "mcp"}, entry.Protocol) || entry.Port < 1 || entry.Port > 65535 ||
			!strings.HasPrefix(entry.Path, "/") || strings.ContainsAny(entry.Path, "?#") {
			return manifest, nil, domain.NewError(400, "manifest_invalid", "入口名称、协议、端口或路径无效")
		}
		entryNames[entry.Name] = true
	}
	return manifest, artifacts, nil
}

func (s *Service) CreateAppVersion(ctx context.Context, subject, appID string, input CreateAppVersionInput) (domain.AppVersion, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.AppVersion{}, err
	}
	if !appIDPattern.MatchString(appID) || !candidateVersionPattern.MatchString(input.Version) ||
		strings.TrimSpace(input.Publisher) == "" || !validAuditURL(input.SourceURL) ||
		!sourceRevisionPattern.MatchString(input.SourceRevision) || strings.TrimSpace(input.LicenseID) == "" || !validAuditURL(input.LicenseURL) {
		return domain.AppVersion{}, domain.NewError(400, "app_version_invalid", "候选版本、发布者、固定来源 revision 或许可证资料不完整")
	}
	seen := make(map[string]bool)
	for _, dependency := range input.Dependencies {
		key := dependency.Kind + "/" + dependency.ID + "@" + dependency.Version
		if !slices.Contains([]string{"model", "plugin", "workflow", "runtime"}, dependency.Kind) ||
			strings.TrimSpace(dependency.ID) == "" || !candidateVersionPattern.MatchString(dependency.Version) ||
			!candidateDigestPattern.MatchString(dependency.SHA256) || !validAuditURL(dependency.SourceURL) ||
			strings.TrimSpace(dependency.LicenseID) == "" || seen[key] {
			return domain.AppVersion{}, domain.NewError(400, "dependency_invalid", "依赖必须声明唯一 ID、版本、来源、许可证与 sha256")
		}
		seen[key] = true
	}
	permissionScopes := make(map[string]bool)
	for _, permission := range input.Permissions {
		if strings.TrimSpace(permission.Scope) == "" || strings.TrimSpace(permission.Reason) == "" || permissionScopes[permission.Scope] {
			return domain.AppVersion{}, domain.NewError(400, "permission_invalid", "权限范围与用途必须明确且不能重复")
		}
		permissionScopes[permission.Scope] = true
	}
	manifest, artifacts, err := validateCandidateManifest(input.Manifest, appID, input.Version, input.Dependencies)
	if err != nil {
		return domain.AppVersion{}, err
	}
	app, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return domain.AppVersion{}, err
	}
	if app.App.GroupID != "" && app.App.GroupID != manifest.GroupID {
		return domain.AppVersion{}, domain.NewError(409, "app_group_mismatch", "Manifest 分组与应用身份分组不一致")
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return domain.AppVersion{}, domain.NewError(400, "manifest_invalid", "清单无法规范化")
	}
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	version := domain.AppVersion{
		AppID: appID, Version: input.Version, UpstreamVersion: strings.TrimSpace(input.UpstreamVersion),
		Publisher: strings.TrimSpace(input.Publisher), SourceURL: input.SourceURL, SourceRevision: input.SourceRevision,
		LicenseID: strings.TrimSpace(input.LicenseID), LicenseURL: input.LicenseURL,
		ManifestRef: "center-manifest://sha256/" + hash, ManifestSHA256: hash, Manifest: canonical,
		Artifacts: artifacts, Dependencies: input.Dependencies, Permissions: input.Permissions,
		Validation: []domain.ValidationCheck{
			{Code: "v1_structure", Status: "passed", Detail: "清单结构与应用身份已校验"},
			{Code: "declared_digests", Status: "passed", Detail: "组件镜像和已声明依赖包含 sha256；制品内容尚未下载核验"},
			{Code: "source_license", Status: "declared", Detail: "来源、固定 revision 和许可证为发布者声明，尚未独立审计"},
			{Code: "artifact_signature", Status: "pending", Detail: "可信制品仓库和签名校验尚未接入"},
			{Code: "station_registration", Status: "pending", Detail: "Station 尚未同步、预检或回报安装状态"},
		},
		Status: "center_recorded", CreatedAt: time.Now().UTC(),
	}
	return s.repository.CreateAppVersion(ctx, version)
}

func (s *Service) ListAppVersions(ctx context.Context, subject, appID string) ([]domain.AppVersion, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListAppVersions(ctx, appID)
}

func (s *Service) AppVersion(ctx context.Context, subject, appID, version string) (domain.AppVersion, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.AppVersion{}, err
	}
	return s.repository.AppVersion(ctx, appID, version)
}
