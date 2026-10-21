package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"gopkg.in/yaml.v3"
)

const (
	taskBuilding  = "building"
	taskPublished = "published"
	taskReleased  = "released"
	taskFailed    = "failed"
)

// SubmitChartInput is a full publication request whose fixed OCI version is
// produced from the supplied chart source instead of being declared.
type SubmitChartInput struct {
	PublicationInput
	ChartArchive string `json:"chartArchive"`
}

func (s *Service) SetMarketRepository(repository MarketRepository) { s.market = repository }

func (s *Service) SubmitChart(ctx context.Context, subject string, input SubmitChartInput) (domain.PublicationTask, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.PublicationTask{}, err
	}
	if s.market == nil {
		return domain.PublicationTask{}, domain.NewError(503, "MARKET_REPOSITORY_UNCONFIGURED", "Market 仓库凭据尚未配置")
	}
	publication := input.PublicationInput
	publication.AppID = strings.TrimSpace(publication.AppID)
	files, chartVersion, err := readChartSource(input.ChartArchive, publication.AppID, publication.Version.Version)
	if err != nil {
		return domain.PublicationTask{}, err
	}
	// Run the fixed-OCI checks now with placeholders for what the release
	// produces, so bad listings never reach Git.
	probe := publication
	probe.Version.SourceRevision = "pending"
	probe.Version.Chart = &CreateOCIChartInput{OCIRef: "oci://" + s.chartRegistryHost + "/charts/" + publication.AppID, Version: chartVersion,
		ManifestDigest: "sha256:" + strings.Repeat("0", 64), PackageDigest: "sha256:" + strings.Repeat("0", 64)}
	if err := s.preflightPublication(ctx, &probe); err != nil {
		return domain.PublicationTask{}, err
	}
	publication.Version.Chart = nil
	publication.Version.SourceRevision = ""
	raw, err := json.Marshal(publication)
	if err != nil {
		return domain.PublicationTask{}, err
	}
	now := s.now().UTC()
	task := domain.PublicationTask{ID: newID("pubtask"), Submitter: subject, AppID: publication.AppID, Version: publication.Version.Version,
		ChartVersion: chartVersion, CreatedAt: now, UpdatedAt: now, Input: raw}
	commit, err := s.market.CommitChart(ctx, publication.AppID, files,
		fmt.Sprintf("chore(charts): publish %s chart %s via Hub MCP\n\nTask: %s\nSubmitted-by: %s", publication.AppID, chartVersion, task.ID, subject))
	if err != nil {
		return domain.PublicationTask{}, err
	}
	task.CommitSHA = commit
	task.Stage = taskBuilding
	if err := s.market.DispatchChartRelease(ctx, publication.AppID); err != nil {
		task.Stage, task.Error = taskFailed, "Chart 已提交但发行触发失败："+errorMessage(err)
	}
	return s.repository.CreatePublicationTask(ctx, task)
}

func (s *Service) PublicationTaskStatus(ctx context.Context, subject, taskID string) (domain.PublicationTask, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.PublicationTask{}, err
	}
	task, err := s.repository.PublicationTask(ctx, taskID)
	if err != nil || task.Submitter != subject {
		return domain.PublicationTask{}, domain.NewError(404, "TASK_NOT_FOUND", "发布任务不存在")
	}
	for task.Stage == taskBuilding || task.Stage == taskPublished {
		previous := task.Stage
		next, changed := s.advancePublicationTask(ctx, subject, task)
		if !changed {
			break
		}
		next.UpdatedAt = s.now().UTC()
		if task, err = s.repository.UpdatePublicationTask(ctx, next); err != nil {
			return domain.PublicationTask{}, err
		}
		if task.Stage == previous {
			break
		}
	}
	return task, nil
}

// advancePublicationTask performs at most one stage transition. Transient
// failures leave the stage unchanged so the next status call retries.
func (s *Service) advancePublicationTask(ctx context.Context, subject string, task domain.PublicationTask) (domain.PublicationTask, bool) {
	switch task.Stage {
	case taskBuilding:
		if s.market == nil {
			return task, false
		}
		run, found, err := s.market.FindChartRelease(ctx, task.CommitSHA)
		if err != nil || !found {
			return task, false
		}
		task.RunID, task.RunURL = run.ID, run.URL
		if run.Status != "completed" {
			return task, true
		}
		if run.Conclusion != "success" {
			task.Stage, task.Error = taskFailed, "Chart 发行流水线未成功（"+run.Conclusion+"）"
			return task, true
		}
		manifest, pkg, err := s.resolveChartDigests(ctx, task.AppID, task.ChartVersion)
		if err != nil {
			return task, false
		}
		task.ManifestDigest, task.PackageDigest, task.Stage, task.Error = manifest, pkg, taskPublished, ""
		return task, true
	case taskPublished:
		var input PublicationInput
		if err := json.Unmarshal(task.Input, &input); err != nil {
			task.Stage, task.Error = taskFailed, "发布资料快照无法读取"
			return task, true
		}
		input.Version.SourceRevision = task.CommitSHA
		input.Version.Chart = &CreateOCIChartInput{OCIRef: "oci://" + s.chartRegistryHost + "/charts/" + task.AppID, Version: task.ChartVersion,
			ManifestDigest: task.ManifestDigest, PackageDigest: task.PackageDigest}
		if input.ExpectedRevision == 0 {
			if managed, err := s.repository.ManagedApp(ctx, task.AppID); err == nil {
				input.ExpectedRevision = managed.App.Revision
			}
		}
		if _, err := s.PublishRegisteredOCI(ctx, subject, input); err != nil {
			var failure *domain.Error
			if errors.As(err, &failure) && failure.Status < 500 {
				task.Stage, task.Error = taskFailed, failure.Code+"："+failure.Message
				return task, true
			}
			task.Error = "发布暂时失败，将在下次查询时重试：" + errorMessage(err)
			return task, true
		}
		task.Stage, task.Error = taskReleased, ""
		return task, true
	}
	return task, false
}

// resolveChartDigests reads the released manifest and its chart layer from the
// registry; CreateOCIAppVersion later re-verifies both against the bytes.
func (s *Service) resolveChartDigests(ctx context.Context, appID, chartVersion string) (string, string, error) {
	raw, err := s.fetchRegistryManifest(ctx, appID, chartVersion, "")
	if err != nil {
		return "", "", err
	}
	var manifest ociManifestPayload
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", "", domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest JSON 无法解析")
	}
	for _, layer := range manifest.Layers {
		if strings.Contains(strings.ToLower(layer.MediaType), "helm.chart.content") {
			return digestOf(raw), strings.ToLower(layer.Digest), nil
		}
	}
	return "", "", domain.NewError(502, "CHART_DIGEST_MISMATCH", "OCI Manifest 未包含 Helm Chart 层")
}

// readChartSource unpacks <appId>/... from a base64 .tgz and checks the chart
// identity; it returns files keyed by their path inside charts/<appId>/.
func readChartSource(encoded, appID, appVersion string) (map[string][]byte, string, error) {
	invalid := func(message string) error { return domain.NewError(422, "CHART_ARCHIVE_INVALID", message) }
	archive, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(archive) == 0 {
		return nil, "", invalid("chartArchive 须为 Chart 目录 .tgz 的 Base64")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, "", invalid("chartArchive 不是有效的 gzip 归档")
	}
	files := map[string][]byte{}
	reader := tar.NewReader(gz)
	total := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", invalid("chartArchive 不是有效的 tar 归档")
		}
		name := header.Name
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || path.Clean(name) != strings.TrimSuffix(name, "/") || strings.HasPrefix(path.Clean(name), "..") {
			return nil, "", invalid("归档包含不安全的路径：" + name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, "", invalid("归档只能包含普通文件：" + name)
		}
		inner, found := strings.CutPrefix(path.Clean(name), appID+"/")
		if !found || inner == "" {
			return nil, "", invalid("归档根目录须为 " + appID + "/")
		}
		data, err := io.ReadAll(io.LimitReader(reader, 8<<20))
		total += len(data)
		if err != nil || total > 8<<20 {
			return nil, "", invalid("归档解压后内容过大")
		}
		files[inner] = data
	}
	var meta chartMetadataYAML
	if err := yaml.Unmarshal(files["Chart.yaml"], &meta); err != nil || files["Chart.yaml"] == nil {
		return nil, "", invalid("归档缺少有效的 Chart.yaml")
	}
	if meta.Name != appID {
		return nil, "", invalid("Chart.yaml 的 name 须等于 appId")
	}
	if !chartVersionPattern.MatchString(meta.Version) {
		return nil, "", invalid("Chart.yaml 的 version 须为 SemVer")
	}
	if fmt.Sprint(meta.AppVersion) != appVersion {
		return nil, "", invalid("Chart.yaml 的 appVersion 须等于 version.version")
	}
	return files, meta.Version, nil
}

func errorMessage(err error) string {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Message
	}
	return "上游服务暂不可用"
}
