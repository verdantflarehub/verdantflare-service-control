package httpapi_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
)

func TestChartUploadReportsBlockersWithoutRegisteringVersion(t *testing.T) {
	server := newTestServer(t, testConfig())
	created := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": "comfyui-audit", "name": "ComfyUI Audit", "groupId": "image", "category": "图像", "summary": "测试 Chart 上传",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create app status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()

	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	contents := "apiVersion: v2\nname: comfyui-audit\ntype: application\nversion: 0.1.0\n"
	if err := tw.WriteHeader(&tar.Header{Name: "comfyui-audit/Chart.yaml", Typeflag: tar.TypeReg, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("chart", "comfyui-audit-0.1.0.tgz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	uploadPath := "/api/control/ops/apps/comfyui-audit/chart-audits"
	upload := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, server.URL+uploadPath, bytes.NewReader(body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := upload()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var report control.ChartAuditReport
	decode(t, response, &report)
	response.Body.Close()
	if report.Deployable || report.StaticChecksPassed || len(report.Checks) == 0 {
		t.Fatalf("unsafe chart was treated as deployable: %+v", report)
	}
	versions := request(t, server, http.MethodGet, "/api/control/ops/apps/comfyui-audit/versions", nil)
	if versions.StatusCode != http.StatusOK {
		t.Fatalf("versions status = %d", versions.StatusCode)
	}
	var records []any
	decode(t, versions, &records)
	versions.Body.Close()
	if len(records) != 0 {
		t.Fatal("upload unexpectedly registered a version")
	}
	switched := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	switched.Body.Close()
	denied := upload()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("role check status = %d, body = %s", denied.StatusCode, readBody(t, denied))
	}
	denied.Body.Close()
}
