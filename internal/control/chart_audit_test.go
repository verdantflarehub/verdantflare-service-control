package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func chartArchiveForTest(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for name, contents := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestChartAuditBlocksPlaceholderAndNeverClaimsDeployable(t *testing.T) {
	archive := chartArchiveForTest(t, map[string]string{
		"comfyui/Chart.yaml":                "apiVersion: v2\nname: comfyui\ntype: application\nversion: 0.1.0\nappVersion: 0.32.0\n",
		"comfyui/values.yaml":               "image:\n  repository: registry.invalid/example/comfyui\n  tag: 0.32.0\n  digest: ''\n  pullPolicy: Always\nresources:\n  requests:\n    nvidia.com/gpu: 1\n  limits:\n    nvidia.com/gpu: 1\nstorage:\n  create: true\n  size: 100Gi\n",
		"comfyui/values.schema.json":        "{}",
		"comfyui/templates/deployment.yaml": "kind: Deployment\n",
		"comfyui/templates/service.yaml":    "kind: Service\nspec:\n  type: ClusterIP\n",
		"comfyui/templates/pvc.yaml":        "kind: PersistentVolumeClaim\n",
	})
	report, err := inspectChart("comfyui", archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Deployable || report.StaticChecksPassed || report.ChartName != "comfyui" || len(report.ArchiveSHA256) != 64 {
		t.Fatalf("incorrect audit result: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "image" && check.Status != "blocked" {
			t.Fatalf("placeholder image was accepted: %+v", check)
		}
		if check.Code == "station_preflight" && check.Status != "pending" {
			t.Fatalf("Station preflight was claimed: %+v", check)
		}
	}
}

func TestChartAuditRejectsTraversalAndLinks(t *testing.T) {
	for _, name := range []string{"comfyui/../secret.yaml", "/etc/passwd", "comfyui/../../secrets"} {
		archive := chartArchiveForTest(t, map[string]string{name: "unexpected"})
		if _, err := inspectChart("comfyui", archive); err == nil {
			t.Fatalf("accepted unsafe path: %q", name)
		}
	}
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "comfyui/Chart.yaml", Typeflag: tar.TypeSymlink, Linkname: strings.Repeat("../", 4) + "secret"}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	if _, err := inspectChart("comfyui", output.Bytes()); err == nil {
		t.Fatal("accepted symbolic link in chart archive")
	}
}
