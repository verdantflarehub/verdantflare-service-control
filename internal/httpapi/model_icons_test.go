package httpapi_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func uploadModelIcon(t *testing.T, server *httptest.Server, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("icon", "provider.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/control/ops/model-icons", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestProviderIconUploadPublishesOnlyAfterModelSave(t *testing.T) {
	repository := store.NewMemoryBootstrap()
	service := control.NewService(repository, &catalogGateway{models: map[string]struct{}{"icon-model": {}}})
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	picture := image.NewRGBA(image.Rect(0, 0, 32, 32))
	picture.Set(0, 0, color.RGBA{R: 48, G: 200, B: 120, A: 255})
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, picture); err != nil {
		t.Fatal(err)
	}
	invalid := uploadModelIcon(t, server, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("SVG upload status = %d, want 400", invalid.StatusCode)
	}
	invalid.Body.Close()

	uploaded := uploadModelIcon(t, server, imageBytes.Bytes())
	if uploaded.StatusCode != http.StatusCreated {
		t.Fatalf("PNG upload status = %d, body = %s", uploaded.StatusCode, readBody(t, uploaded))
	}
	var icon control.ModelProviderIconUpload
	decode(t, uploaded, &icon)
	uploaded.Body.Close()
	if icon.Width != 32 || icon.Height != 32 || len(icon.SHA256) != 64 {
		t.Fatalf("unexpected upload response: %+v", icon)
	}

	asset := request(t, server, http.MethodGet, icon.ProviderIconURL, nil)
	served, err := io.ReadAll(asset.Body)
	asset.Body.Close()
	if err != nil || asset.StatusCode != http.StatusOK || asset.Header.Get("Content-Type") != "image/png" || !bytes.Equal(served, imageBytes.Bytes()) {
		t.Fatalf("public icon read failed: status=%d content-type=%s error=%v", asset.StatusCode, asset.Header.Get("Content-Type"), err)
	}
	if asset.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("icon missing immutable cache policy: %q", asset.Header.Get("Cache-Control"))
	}

	model := domain.PublicModel{ID: "icon-model", Name: "Icon Model", Provider: "Provider", ProviderIconURL: icon.ProviderIconURL, Summary: "Published description", Categories: []string{"文本生成"}}
	created := request(t, server, http.MethodPost, "/api/control/ops/models", model)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create with uploaded icon: %d %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()
	public := request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	var catalog domain.PublicCatalog
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Models) != 0 {
		t.Fatalf("draft leaked to public catalog: %+v", catalog.Models)
	}
	model.PublicVisible = true
	published := request(t, server, http.MethodPatch, "/api/control/ops/models/icon-model", model)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish with uploaded icon: %d %s", published.StatusCode, readBody(t, published))
	}
	published.Body.Close()
	public = request(t, server, http.MethodGet, "/api/control/public/catalog", nil)
	decode(t, public, &catalog)
	public.Body.Close()
	if len(catalog.Models) != 1 || catalog.Models[0].ProviderIconURL != icon.ProviderIconURL {
		t.Fatalf("published icon not projected: %+v", catalog.Models)
	}

	missing := model
	missing.ProviderIconURL = "/api/control/public/model-icons/" + strings.Repeat("a", 64)
	rejected := request(t, server, http.MethodPatch, "/api/control/ops/models/icon-model", missing)
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("nonexistent uploaded icon status = %d, want 400", rejected.StatusCode)
	}
	rejected.Body.Close()
}

func TestProviderIconUploadRequiresLogin(t *testing.T) {
	service := control.NewService(store.NewMemoryBootstrap())
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	response := uploadModelIcon(t, server, []byte("not an image"))
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous icon upload status = %d, want 401", response.StatusCode)
	}
}

func TestProviderIconUploadRequiresModelOperationsRole(t *testing.T) {
	repository := store.NewMemorySeeded(time.Now().UTC())
	const subject = "00000000-0000-4000-8000-000000000001"
	if _, err := repository.SetActiveOrganization(context.Background(), subject, "org_northshore"); err != nil {
		t.Fatal(err)
	}
	service := control.NewService(repository)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", DevLoginSubject: subject}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	response := uploadModelIcon(t, server, []byte("not an image"))
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-operator icon upload status = %d, want 403", response.StatusCode)
	}
}
