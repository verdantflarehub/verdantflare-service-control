package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"gopkg.in/yaml.v3"
)

const maxOCIManifestBytes = 1 << 20

var unsafeSVGPattern = regexp.MustCompile(`(?is)<\s*script|<\s*foreignobject|on[a-z]+\s*=|javascript:|(?:href|xlink:href)\s*=\s*["']\s*https?:`)

type CreateOCIChartInput struct {
	OCIRef         string `json:"ociRef"`
	Version        string `json:"version"`
	ManifestDigest string `json:"manifestDigest"`
	PackageDigest  string `json:"packageDigest"`
}

type CreateOCIAppVersionInput struct {
	Version         string               `json:"version"`
	Chart           *CreateOCIChartInput `json:"chart"`
	ReleaseNotes    string               `json:"releaseNotes"`
	SourceRevision  string               `json:"sourceRevision"`
	UpstreamVersion string               `json:"upstreamVersion,omitempty"`
	Publisher       string               `json:"publisher,omitempty"`
	SourceURL       string               `json:"sourceUrl,omitempty"`
	LicenseID       string               `json:"licenseId,omitempty"`
	LicenseURL      string               `json:"licenseUrl,omitempty"`
}

type ociManifestPayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

type chartMetadataYAML struct {
	APIVersion   string            `yaml:"apiVersion"`
	Name         string            `yaml:"name"`
	Type         string            `yaml:"type"`
	Version      string            `yaml:"version"`
	AppVersion   string            `yaml:"appVersion"`
	KubeVersion  string            `yaml:"kubeVersion"`
	Icon         string            `yaml:"icon"`
	Annotations  map[string]string `yaml:"annotations"`
	Dependencies []struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"dependencies"`
}

type chartValuesYAML struct {
	Image struct {
		Component  string `yaml:"component"`
		Repository string `yaml:"repository"`
		Tag        string `yaml:"tag"`
		Digest     string `yaml:"digest"`
	} `yaml:"image"`
	Images       []domain.AppImageRef `yaml:"images"`
	Models       []string             `yaml:"models"`
	Capabilities []string             `yaml:"capabilities"`
}

func (s *Service) CreateOCIAppVersion(ctx context.Context, subject, appID string, input CreateOCIAppVersionInput) (domain.AppVersion, bool, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.AppVersion{}, false, err
	}
	appID = strings.TrimSpace(appID)
	if !appIDPattern.MatchString(appID) {
		return domain.AppVersion{}, false, domain.NewError(400, "app_id_invalid", "应用 ID 格式无效")
	}
	managed, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return domain.AppVersion{}, false, err
	}
	version := strings.TrimSpace(input.Version)
	if !chartVersionPattern.MatchString(version) || strings.EqualFold(version, "latest") {
		return domain.AppVersion{}, false, domain.NewError(422, "VERSION_INVALID", "发行版本必须是有效的 SemVer，禁止使用 latest")
	}
	if input.Chart == nil {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_REQUIRED", "必须提供 chart OCI 引用与摘要")
	}
	ociRef := strings.TrimSpace(input.Chart.OCIRef)
	chartVer := strings.TrimSpace(input.Chart.Version)
	manifestDigest := strings.ToLower(strings.TrimSpace(input.Chart.ManifestDigest))
	packageDigest := strings.ToLower(strings.TrimSpace(input.Chart.PackageDigest))

	expectedPrefix := "oci://" + s.chartRegistryHost + "/charts/"
	expectedRef := expectedPrefix + appID
	if strings.ContainsAny(ociRef, "?#@") || strings.Count(strings.TrimPrefix(ociRef, "oci://"), ":") > 0 {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_OCI_REF_INVALID", "chart.ociRef 不能包含 tag、凭据、查询参数或 fragment")
	}
	if strings.HasPrefix(ociRef, expectedPrefix) && ociRef != expectedRef {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_IDENTITY_MISMATCH", "chart.ociRef 的 Chart 名称必须与应用 ID 一致")
	}
	if ociRef != expectedRef {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_OCI_REF_INVALID", "chart.ociRef 必须指向受控 Registry 仓库 "+expectedRef)
	}
	if !chartVersionPattern.MatchString(chartVer) || strings.EqualFold(chartVer, "latest") {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_VERSION_INVALID", "chart.version 必须是有效的 SemVer")
	}
	if !imageDigestPattern.MatchString(manifestDigest) || (packageDigest != "" && !imageDigestPattern.MatchString(packageDigest)) {
		return domain.AppVersion{}, false, domain.NewError(422, "CHART_DIGEST_INVALID", "manifestDigest 与 packageDigest 必须为 sha256:<64位小写十六进制>")
	}

	verified, iconToSave, err := s.verifyAndExtractOCIChart(ctx, managed.App, ociRef, version, chartVer, manifestDigest, packageDigest, strings.TrimSpace(input.ReleaseNotes), strings.TrimSpace(input.SourceRevision))
	if err != nil {
		return domain.AppVersion{}, false, err
	}
	verified.UpstreamVersion = strings.TrimSpace(input.UpstreamVersion)
	verified.Publisher = strings.TrimSpace(input.Publisher)
	verified.SourceURL = strings.TrimSpace(input.SourceURL)
	verified.LicenseID = strings.TrimSpace(input.LicenseID)
	verified.LicenseURL = strings.TrimSpace(input.LicenseURL)

	if existing, err := s.repository.AppVersion(ctx, appID, version); err == nil {
		if existing.Chart != nil &&
			existing.Chart.OCIRef == verified.Chart.OCIRef &&
			existing.Chart.Version == verified.Chart.Version &&
			existing.Chart.ManifestDigest == verified.Chart.ManifestDigest &&
			existing.Chart.PackageDigest == verified.Chart.PackageDigest &&
			existing.ReleaseNotes == verified.ReleaseNotes &&
			existing.SourceRevision == verified.SourceRevision &&
			existing.UpstreamVersion == verified.UpstreamVersion &&
			existing.Publisher == verified.Publisher &&
			existing.SourceURL == verified.SourceURL &&
			existing.LicenseID == verified.LicenseID &&
			existing.LicenseURL == verified.LicenseURL {
			return existing, false, nil
		}
		return domain.AppVersion{}, false, domain.NewError(409, "VERSION_CONFLICT", "该发行版本已经绑定其他制品，请使用新版本号")
	}

	if iconToSave != nil {
		if err := s.repository.PutModelProviderIcon(ctx, *iconToSave); err != nil {
			return domain.AppVersion{}, false, err
		}
	}
	created, err := s.repository.CreateAppVersion(ctx, verified)
	if err != nil {
		if domErr, ok := err.(*domain.Error); ok && domErr.Status == http.StatusConflict {
			return domain.AppVersion{}, false, domain.NewError(409, "VERSION_CONFLICT", "该发行版本已经绑定其他制品，请使用新版本号")
		}
		return domain.AppVersion{}, false, err
	}
	return created, true, nil
}

func (s *Service) verifyAndExtractOCIChart(
	ctx context.Context,
	app domain.App,
	ociRef, marketVersion, chartVer, manifestDigest, packageDigest, releaseNotes, sourceRevision string,
) (domain.AppVersion, *domain.ModelProviderIcon, error) {
	manifestBytes, err := s.fetchRegistryManifest(ctx, app.ID, chartVer, manifestDigest)
	if err != nil {
		return domain.AppVersion{}, nil, err
	}

	var manifest ociManifestPayload
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return domain.AppVersion{}, nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest JSON 无法解析")
	}
	var layerDigest string
	var layerSize int64
	for _, layer := range manifest.Layers {
		mt := strings.ToLower(strings.TrimSpace(layer.MediaType))
		if strings.Contains(mt, "helm.chart.content") || strings.Contains(mt, "tar+gzip") || strings.Contains(mt, "x-tgz") {
			layerDigest = strings.ToLower(strings.TrimSpace(layer.Digest))
			layerSize = layer.Size
			break
		}
	}
	if layerDigest == "" {
		return domain.AppVersion{}, nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest 未包含 Helm Chart .tgz 层")
	}
	if packageDigest == "" {
		packageDigest = layerDigest
	}
	if layerDigest != packageDigest {
		return domain.AppVersion{}, nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest 中的 Chart 层摘要与提交的 packageDigest 不一致")
	}

	blobBytes, err := s.fetchRegistryBlob(ctx, app.ID, packageDigest, layerSize)
	if err != nil {
		return domain.AppVersion{}, nil, err
	}

	files, err := readChartArchive(blobBytes)
	if err != nil {
		return domain.AppVersion{}, nil, domain.NewError(422, "CHART_ARCHIVE_INVALID", "Chart 归档无效、超限或包含不安全路径")
	}
	chartRaw, hasChart := files["Chart.yaml"]
	if !hasChart {
		return domain.AppVersion{}, nil, domain.NewError(422, "CHART_ARCHIVE_INVALID", "Chart 归档缺少 Chart.yaml")
	}
	var meta chartMetadataYAML
	if err := yaml.Unmarshal(chartRaw, &meta); err != nil {
		return domain.AppVersion{}, nil, domain.NewError(422, "CHART_ARCHIVE_INVALID", "Chart.yaml 无法解析")
	}
	if strings.TrimSpace(meta.Name) != app.ID || strings.TrimSpace(meta.Version) != chartVer {
		return domain.AppVersion{}, nil, domain.NewError(422, "CHART_IDENTITY_MISMATCH", "包内 Chart.yaml 的 name 或 version 与请求声明不一致")
	}

	var values chartValuesYAML
	if valuesRaw, ok := files["values.yaml"]; ok {
		_ = yaml.Unmarshal(valuesRaw, &values)
	}

	images, err := extractChartImages(meta, values, app.Images)
	if err != nil {
		return domain.AppVersion{}, nil, err
	}
	models := extractStringListAnnotation(meta.Annotations, "verdantflare.com/models", values.Models, app.Models)
	if len(models) == 0 {
		models = extractStringListAnnotation(meta.Annotations, "verdantflare.ai/models", values.Models, app.Models)
	}
	capabilities := extractStringListAnnotation(meta.Annotations, "verdantflare.com/capabilities", values.Capabilities, app.Capabilities)
	if len(capabilities) == 0 {
		capabilities = extractStringListAnnotation(meta.Annotations, "verdantflare.ai/capabilities", values.Capabilities, app.Capabilities)
	}

	deps := make([]domain.AppDependency, 0, len(meta.Dependencies))
	for _, dep := range meta.Dependencies {
		if strings.TrimSpace(dep.Name) != "" {
			deps = append(deps, domain.AppDependency{
				AppID:        strings.TrimSpace(dep.Name),
				VersionRange: strings.TrimSpace(dep.Version),
			})
		}
	}

	var appVersionPtr *string
	if trimmed := strings.TrimSpace(meta.AppVersion); trimmed != "" {
		appVersionPtr = &trimmed
	}
	var kubeRangePtr *string
	if trimmed := strings.TrimSpace(meta.KubeVersion); trimmed != "" {
		kubeRangePtr = &trimmed
	}
	var minRuntimePtr *string
	if trimmed := strings.TrimSpace(meta.Annotations["verdantflare.com/minimum-runtime-version"]); trimmed != "" {
		minRuntimePtr = &trimmed
	} else if trimmed := strings.TrimSpace(meta.Annotations["verdantflare.ai/min-station-version"]); trimmed != "" {
		minRuntimePtr = &trimmed
	}
	archs := extractStringListAnnotation(meta.Annotations, "verdantflare.com/architectures", nil, nil)
	if len(archs) == 0 {
		archs = extractStringListAnnotation(meta.Annotations, "verdantflare.ai/architectures", nil, []string{"amd64"})
	}
	if len(archs) == 0 {
		archs = []string{"amd64"}
	}

	logoRef, iconRecord, err := extractAndValidateChartLogo(files, app.Logo)
	if err != nil {
		return domain.AppVersion{}, nil, err
	}

	now := s.now().UTC()
	nowStr := now.Format(time.RFC3339)
	return domain.AppVersion{
		AppID:   app.ID,
		Version: marketVersion,
		Chart: &domain.AppVersionChart{
			OCIRef:           ociRef,
			Name:             meta.Name,
			Version:          meta.Version,
			AppVersion:       appVersionPtr,
			ManifestDigest:   manifestDigest,
			PackageDigest:    packageDigest,
			PackageSizeBytes: int64(len(blobBytes)),
		},
		Images:       images,
		Models:       models,
		Capabilities: capabilities,
		Dependencies: deps,
		Compatibility: domain.AppCompatibility{
			KubernetesVersionRange: kubeRangePtr,
			MinimumRuntimeVersion:  minRuntimePtr,
			Architectures:          archs,
		},
		ReleaseNotes:   releaseNotes,
		Logo:           logoRef,
		SourceRevision: sourceRevision,
		VerifiedAt:     nowStr,
		Status:         "verified",
		CreatedAt:      now,
	}, iconRecord, nil
}

func (s *Service) fetchRegistryManifest(ctx context.Context, appID, reference, expectedDigest string) ([]byte, error) {
	manifestURL := fmt.Sprintf("%s/v2/charts/%s/manifests/%s", s.chartRegistryURL, url.PathEscape(appID), url.PathEscape(reference))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "无法构造 Registry 请求")
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	client := s.chartHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "Chart Registry 暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, domain.NewError(422, "CHART_NOT_FOUND", "Registry 中未找到目标 Chart 版本或 Manifest")
	}
	if resp.StatusCode >= 500 {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "Chart Registry 服务不可用")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, domain.NewError(502, "REGISTRY_UNAVAILABLE", "Chart Registry Manifest 响应异常")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOCIManifestBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxOCIManifestBytes {
		return nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest 内容无效或超限")
	}
	// An empty expectation resolves a tag; callers then verify the digests.
	if expectedDigest != "" && digestOf(raw) != expectedDigest {
		return nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest 实际 SHA-256 与提交的 manifestDigest 不一致")
	}
	return raw, nil
}

func (s *Service) fetchRegistryBlob(ctx context.Context, appID, packageDigest string, expectedSize int64) ([]byte, error) {
	blobURL := fmt.Sprintf("%s/v2/charts/%s/blobs/%s", s.chartRegistryURL, url.PathEscape(appID), url.PathEscape(packageDigest))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "无法构造 Registry Blob 请求")
	}
	client := s.chartHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "Chart Registry 暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, domain.NewError(422, "CHART_NOT_FOUND", "Registry 中未找到目标 Chart 归档层")
	}
	if resp.StatusCode >= 500 {
		return nil, domain.NewError(503, "REGISTRY_UNAVAILABLE", "Chart Registry 服务不可用")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, domain.NewError(502, "REGISTRY_UNAVAILABLE", "Chart Registry Blob 响应异常")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 2<<20 {
		return nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "Chart 归档层大小无效或超过 2 MiB")
	}
	if expectedSize > 0 && int64(len(raw)) != expectedSize {
		return nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "Chart 归档层实际字节数与 Manifest 声明不一致")
	}
	sum := sha256.Sum256(raw)
	actualDigest := "sha256:" + hex.EncodeToString(sum[:])
	if actualDigest != packageDigest {
		return nil, domain.NewError(502, "CHART_DIGEST_MISMATCH", "Chart 归档层实际 SHA-256 与提交的 packageDigest 不一致")
	}
	return raw, nil
}

func extractChartImages(meta chartMetadataYAML, values chartValuesYAML, fallback []domain.AppImageRef) ([]domain.AppImageRef, error) {
	var images []domain.AppImageRef
	if raw := strings.TrimSpace(meta.Annotations["verdantflare.com/images"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &images); err != nil {
			_ = yaml.Unmarshal([]byte(raw), &images)
		}
	}
	if len(images) == 0 && len(values.Images) > 0 {
		images = slices.Clone(values.Images)
	}
	if len(images) == 0 && strings.TrimSpace(values.Image.Repository) != "" && strings.TrimSpace(values.Image.Tag) != "" {
		comp := strings.TrimSpace(values.Image.Component)
		if comp == "" {
			comp = "app"
		}
		images = []domain.AppImageRef{
			{
				Component: comp,
				Image:     strings.TrimSpace(values.Image.Repository) + ":" + strings.TrimSpace(values.Image.Tag),
			},
		}
	}
	if len(images) == 0 && len(fallback) > 0 {
		images = slices.Clone(fallback)
	}
	if images == nil {
		images = []domain.AppImageRef{}
	}
	for _, item := range images {
		ref := strings.TrimSpace(item.Image)
		if ref == "" || strings.HasSuffix(strings.ToLower(ref), ":latest") || !strings.Contains(ref, ":") {
			return nil, domain.NewError(422, "IMAGE_TAG_INVALID", "Chart 镜像地址必须包含明确版本标签，禁止无标签或 latest")
		}
	}
	return images, nil
}

func extractStringListAnnotation(annotations map[string]string, key string, valuesList, fallback []string) []string {
	if raw := strings.TrimSpace(annotations[key]); raw != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			return parsed
		}
		parts := strings.Split(raw, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	if len(valuesList) > 0 {
		return slices.Clone(valuesList)
	}
	if len(fallback) > 0 {
		return slices.Clone(fallback)
	}
	return []string{}
}

func extractAndValidateChartLogo(files map[string][]byte, fallback *domain.LogoRef) (*domain.LogoRef, *domain.ModelProviderIcon, error) {
	for _, candidate := range []struct {
		name      string
		mediaType string
	}{
		{"logo.svg", "image/svg+xml"},
		{"icon.svg", "image/svg+xml"},
		{"logo.png", "image/png"},
		{"icon.png", "image/png"},
	} {
		data, ok := files[candidate.name]
		if !ok {
			continue
		}
		if len(data) == 0 || len(data) > maxModelIconBytes {
			return nil, nil, domain.NewError(422, "CHART_LOGO_INVALID", "包内 Logo 大小无效或超过 1 MiB")
		}
		sum := sha256.Sum256(data)
		hexDigest := hex.EncodeToString(sum[:])
		if candidate.mediaType == "image/svg+xml" {
			if !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "<svg") || unsafeSVGPattern.Match(data) {
				return nil, nil, domain.NewError(422, "CHART_LOGO_INVALID", "包内 SVG Logo 包含不安全脚本或外部引用")
			}
			icon := &domain.ModelProviderIcon{
				SHA256:      hexDigest,
				ContentType: "image/svg+xml",
				Data:        append([]byte(nil), data...),
				Width:       64,
				Height:      64,
			}
			return &domain.LogoRef{
				URL:       modelIconPathPrefix + hexDigest,
				Digest:    "sha256:" + hexDigest,
				MediaType: "image/svg+xml",
			}, icon, nil
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" || cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 4096 || cfg.Height > 4096 {
			return nil, nil, domain.NewError(422, "CHART_LOGO_INVALID", "包内 PNG Logo 尺寸或格式无效")
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return nil, nil, domain.NewError(422, "CHART_LOGO_INVALID", "包内 PNG Logo 无法解码")
		}
		icon := &domain.ModelProviderIcon{
			SHA256:      hexDigest,
			ContentType: "image/png",
			Data:        append([]byte(nil), data...),
			Width:       cfg.Width,
			Height:      cfg.Height,
		}
		return &domain.LogoRef{
			URL:       modelIconPathPrefix + hexDigest,
			Digest:    "sha256:" + hexDigest,
			MediaType: "image/png",
		}, icon, nil
	}
	if fallback != nil {
		copyLogo := *fallback
		return &copyLogo, nil, nil
	}
	return nil, nil, nil
}

func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
