package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"gopkg.in/yaml.v3"
)

const maxChartExpandedBytes = 8 << 20

var chartVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
var imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var forbiddenKindPattern = regexp.MustCompile(`(?im)^\s*kind:\s*(Secret|Ingress|IngressRoute)\s*(?:#.*)?$`)
var forbiddenServicePattern = regexp.MustCompile(`(?im)^\s*type:\s*(NodePort|LoadBalancer)\s*(?:#.*)?$`)
var embeddedCredentialPattern = regexp.MustCompile(`(?im)^\s*(password|token|access[_-]?key|secret[_-]?key)\s*:\s*\S+`)

type ChartAuditCheck struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type ChartAuditReport struct {
	AppID              string            `json:"appId"`
	ChartName          string            `json:"chartName"`
	ChartVersion       string            `json:"chartVersion"`
	AppVersion         string            `json:"appVersion"`
	ArchiveSHA256      string            `json:"archiveSha256"`
	StaticChecksPassed bool              `json:"staticChecksPassed"`
	Deployable         bool              `json:"deployable"`
	Checks             []ChartAuditCheck `json:"checks"`
}

func InvalidChartUpload() error {
	return domain.NewError(400, "chart_upload_invalid", "请上传不超过 2 MiB 的 .tgz Helm Chart 文件")
}

func invalidChartArchive() error {
	return domain.NewError(400, "chart_archive_invalid", "Chart 归档无效、超限或包含不安全文件")
}

func (s *Service) AuditAppChart(ctx context.Context, subject, appID string, archive []byte) (ChartAuditReport, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return ChartAuditReport{}, err
	}
	app, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return ChartAuditReport{}, err
	}
	return inspectChart(appID, app.App.GroupID, archive)
}

func inspectChart(appID, groupID string, archive []byte) (ChartAuditReport, error) {
	hash := sha256.Sum256(archive)
	report := ChartAuditReport{AppID: appID, ArchiveSHA256: hex.EncodeToString(hash[:]), Checks: []ChartAuditCheck{}}
	files, err := readChartArchive(archive)
	if err != nil {
		return ChartAuditReport{}, invalidChartArchive()
	}
	add := func(code string, passed bool, detail string) {
		status := "blocked"
		if passed {
			status = "passed"
		}
		report.Checks = append(report.Checks, ChartAuditCheck{Code: code, Status: status, Detail: detail})
	}
	metadata := struct {
		APIVersion   string `yaml:"apiVersion"`
		Name         string `yaml:"name"`
		Type         string `yaml:"type"`
		Version      string `yaml:"version"`
		AppVersion   string `yaml:"appVersion"`
		Dependencies []any  `yaml:"dependencies"`
	}{}
	chartBytes, hasChart := files["Chart.yaml"]
	chartValid := hasChart && yaml.Unmarshal(chartBytes, &metadata) == nil && metadata.APIVersion == "v2" && metadata.Type == "application" && chartVersionPattern.MatchString(metadata.Version) && strings.TrimSpace(metadata.AppVersion) != ""
	report.ChartName, report.ChartVersion, report.AppVersion = metadata.Name, metadata.Version, metadata.AppVersion
	add("chart_metadata", chartValid, "需要 Chart.yaml：apiVersion v2、application 类型、固定语义化 Chart 版本和 appVersion")
	add("app_identity", chartValid && metadata.Name == appID, "Chart 名称须与 Hub 应用 ID 一致")

	values := struct {
		Image struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
			Digest     string `yaml:"digest"`
			PullPolicy string `yaml:"pullPolicy"`
		} `yaml:"image"`
		Resources struct {
			Requests map[string]any `yaml:"requests"`
			Limits   map[string]any `yaml:"limits"`
		} `yaml:"resources"`
		Storage struct {
			Create           bool   `yaml:"create"`
			ExistingClaim    string `yaml:"existingClaim"`
			Size             string `yaml:"size"`
			StorageClassName string `yaml:"storageClassName"`
		} `yaml:"storage"`
	}{}
	valueBytes, hasValues := files["values.yaml"]
	valuesValid := hasValues && yaml.Unmarshal(valueBytes, &values) == nil
	add("values_parse", valuesValid, "values.yaml 须存在且为可解析的 YAML")
	add("image", valuesValid && values.Image.Repository != "" && !strings.Contains(values.Image.Repository, ".invalid/") && chartVersionPattern.MatchString(values.Image.Tag) && imageDigestPattern.MatchString(values.Image.Digest) && values.Image.PullPolicy == "Always", "镜像须使用真实仓库、固定语义化标签、SHA-256 声明和 Always 拉取策略；还需独立验证摘要与仓库字节")
	deploymentBytes, deployment := files["templates/deployment.yaml"]
	serviceBytes, service := files["templates/service.yaml"]
	pvcBytes, pvc := files["templates/pvc.yaml"]
	workloadPresent := deployment && service && (pvc || values.Storage.ExistingClaim != "") && bytes.Contains(deploymentBytes, []byte("kind: Deployment")) && bytes.Contains(serviceBytes, []byte("kind: Service")) && bytes.Contains(serviceBytes, []byte("type: ClusterIP")) && (!pvc || bytes.Contains(pvcBytes, []byte("kind: PersistentVolumeClaim")))
	add("workload_templates", workloadPresent, "需要 Deployment、显式 ClusterIP Service，以及 PVC 模板或已声明的现有 PVC")
	if groupID == "agent" {
		add("gpu_resources", valuesValid && values.Resources.Requests["nvidia.com/gpu"] == nil && values.Resources.Limits["nvidia.com/gpu"] == nil, "Agent 目录候选默认不申请 GPU；如需本地模型，另行审核资源与目标 Station")
	} else {
		add("gpu_resources", valuesValid && positiveResource(values.Resources.Requests["nvidia.com/gpu"]) && positiveResource(values.Resources.Limits["nvidia.com/gpu"]), "GPU 应用须明确声明 requests/limits；目标 Station 仍需容量预检")
	}
	add("storage", valuesValid && ((values.Storage.Create && pvc && values.Storage.Size != "" && values.Storage.StorageClassName != "") || values.Storage.ExistingClaim != ""), "须指定工作卷容量及目标 StorageClass，或提供已授权的现有 PVC；目标容量仍需预检")
	unsafe := embeddedCredentialPattern.Match(valueBytes)
	dependencies := len(metadata.Dependencies) > 0
	for filename, contents := range files {
		if strings.HasPrefix(filename, "templates/") && (forbiddenKindPattern.Match(contents) || forbiddenServicePattern.Match(contents)) {
			unsafe = true
		}
		lower := strings.ToLower(filename)
		if strings.Contains(lower, "secret") || strings.HasSuffix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".crt") {
			unsafe = true
		}
		if strings.HasPrefix(filename, "charts/") {
			dependencies = true
		}
	}
	add("exposure_and_secrets", !unsafe, "Chart 不得携带凭据、Secret 文件、Ingress 或公网 Service；此项仅为静态扫描，不能证明模板渲染安全")
	add("dependencies", !dependencies, "当前预检仅支持无子 Chart 依赖；依赖内容须另行递归审核")
	schemaBytes, schema := files["values.schema.json"]
	var schemaValue map[string]any
	add("values_schema", schema && json.Unmarshal(schemaBytes, &schemaValue) == nil && len(schemaValue) > 0, "建议提供有效的 values.schema.json 约束安装输入")
	report.StaticChecksPassed = true
	for _, check := range report.Checks {
		if check.Status == "blocked" {
			report.StaticChecksPassed = false
		}
	}
	report.Checks = append(report.Checks,
		ChartAuditCheck{Code: "helm_render", Status: "pending", Detail: "Control 未执行不受信任模板；需在隔离环境完成 helm lint/template 与策略校验"},
		ChartAuditCheck{Code: "artifact_trust", Status: "pending", Detail: "需将原始 Chart 归档存入受控不可变仓库，核验镜像、来源、许可证、摘要及签名"},
		ChartAuditCheck{Code: "station_preflight", Status: "pending", Detail: "需目标 Station 核对架构、GPU/驱动、存储、网络、权限，并完成安装、健康与工作流验证"},
	)
	// A transient static report can never authorize publication or installation.
	report.Deployable = false
	return report, nil
}

func positiveResource(value any) bool {
	switch number := value.(type) {
	case int:
		return number > 0
	case uint64:
		return number > 0
	case float64:
		return number > 0
	}
	return false
}

func readChartArchive(archive []byte) (map[string][]byte, error) {
	if len(archive) == 0 || len(archive) > 2<<20 {
		return nil, errors.New("compressed size")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	treader := tar.NewReader(io.LimitReader(gz, maxChartExpandedBytes+(1<<20)))
	files := make(map[string][]byte)
	root := ""
	total := int64(0)
	entries := 0
	for {
		header, err := treader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		entries++
		if entries > 128 || header.Name == "" || strings.HasPrefix(header.Name, "/") || strings.Contains(header.Name, "\\") || path.Clean(header.Name) != strings.TrimSuffix(header.Name, "/") || strings.Contains("/"+header.Name+"/", "/../") {
			return nil, errors.New("unsafe path")
		}
		parts := strings.Split(strings.TrimSuffix(header.Name, "/"), "/")
		if len(parts) < 1 || parts[0] == "." || (len(parts) == 1 && header.Typeflag != tar.TypeDir) {
			return nil, errors.New("root directory required")
		}
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root {
			return nil, errors.New("multiple chart roots")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, errors.New("non regular file")
		}
		if header.Size < 0 || header.Size > maxChartExpandedBytes-total {
			return nil, errors.New("expanded size")
		}
		total += header.Size
		filename := strings.Join(parts[1:], "/")
		if _, exists := files[filename]; exists {
			return nil, errors.New("duplicate entry")
		}
		contents, err := io.ReadAll(io.LimitReader(treader, header.Size+1))
		if err != nil || int64(len(contents)) != header.Size {
			return nil, errors.New("truncated entry")
		}
		files[filename] = contents
	}
	if len(files) == 0 || root == "" {
		return nil, errors.New("empty chart")
	}
	return files, nil
}
