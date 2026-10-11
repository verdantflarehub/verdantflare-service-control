package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestAgentChartAuditDoesNotRequireGPU(t *testing.T) {
	files := map[string]string{
		"Chart.yaml":                "apiVersion: v2\nname: hermes-agent\ntype: application\nversion: 0.1.0\nappVersion: 1.0.0\n",
		"values.yaml":               "image:\n  repository: registry.example.test/hermes-agent\n  tag: 1.0.0\n  digest: sha256:" + strings.Repeat("a", 64) + "\n  pullPolicy: Always\nresources:\n  requests: {cpu: 1}\n  limits: {cpu: 2}\nstorage:\n  create: true\n  size: 10Gi\n  storageClassName: standard\n",
		"values.schema.json":        `{"type":"object"}`,
		"templates/deployment.yaml": "kind: Deployment\n",
		"templates/service.yaml":    "kind: Service\ntype: ClusterIP\n",
		"templates/pvc.yaml":        "kind: PersistentVolumeClaim\n",
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: "hermes-agent/" + name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		group string
		want  string
	}{{"agent", "passed"}, {"image", "blocked"}} {
		report, err := inspectChart("hermes-agent", test.group, archive.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range report.Checks {
			if check.Code == "gpu_resources" {
				if check.Status != test.want {
					t.Fatalf("group %s GPU check = %s, want %s", test.group, check.Status, test.want)
				}
				goto next
			}
		}
		t.Fatalf("group %s missing GPU check", test.group)
	next:
	}
}
