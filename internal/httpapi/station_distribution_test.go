package httpapi_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestInternalStationCandidateDistributionRequiresGrantAndVerifiedCertificate(t *testing.T) {
	repository := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
	server := httpapi.New(testConfig(), control.NewService(repository), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ops := httptest.NewServer(server)
	t.Cleanup(ops.Close)
	const appID, version = "internal-candidate", "1.0.0"
	const stationID = "0199f879-1234-7abc-8abc-0123456789ab"
	created := request(t, ops, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": appID, "name": "Internal Candidate", "groupId": "image", "category": "Image", "summary": "内部测试",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()
	invalidSelection := request(t, ops, http.MethodPatch, "/api/control/ops/apps/"+appID, map[string]any{
		"name": "Internal Candidate", "groupId": "image", "version": version, "channel": "Candidate",
	})
	if invalidSelection.StatusCode != http.StatusConflict {
		t.Fatalf("unregistered directory version accepted: %d %s", invalidSelection.StatusCode, readBody(t, invalidSelection))
	}
	invalidSelection.Body.Close()
	image := "registry.example.test/workflow:v1.0.0@sha256:" + strings.Repeat("a", 64)
	versionPath := "/api/control/ops/apps/" + appID + "/versions"
	registered := request(t, ops, http.MethodPost, versionPath, map[string]any{
		"version": version, "publisher": "VerdantFlare", "sourceUrl": "https://example.test/workflow",
		"sourceRevision": strings.Repeat("b", 40), "licenseId": "Apache-2.0",
		"licenseUrl": "https://example.test/LICENSE", "manifest": candidateManifestForTest(appID, version, image),
		"dependencies": []any{}, "permissions": []any{},
	})
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("register version: %d %s", registered.StatusCode, readBody(t, registered))
	}
	var sealed domain.AppVersion
	decode(t, registered, &sealed)
	registered.Body.Close()

	uri, _ := url.Parse("spiffe://verdantflarehub.com/station/" + stationID)
	cert := &x509.Certificate{Raw: []byte("test station certificate"), URIs: []*url.URL{uri}}
	fingerprint := sha256.Sum256(cert.Raw)
	fingerprintText := hex.EncodeToString(fingerprint[:])
	grantPath := versionPath + "/" + version + "/station-grants"
	granted := request(t, ops, http.MethodPost, grantPath, map[string]any{
		"stationId": stationID, "organizationId": "org_verdantflare",
		"certificateSha256": fingerprintText, "expiresAt": time.Now().Add(24 * time.Hour).UTC(),
	})
	if granted.StatusCode != http.StatusCreated {
		t.Fatalf("create grant: %d %s", granted.StatusCode, readBody(t, granted))
	}
	granted.Body.Close()

	public := request(t, ops, http.MethodGet, "/api/control/station/v1/catalog", nil)
	if public.StatusCode != http.StatusNotFound {
		t.Fatalf("public listener exposed Station route: %d", public.StatusCode)
	}
	public.Body.Close()

	stationHandler := server.StationHandler()
	stationRequest := func(path string, tlsState *tls.ConnectionState) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.TLS = tlsState
		response := httptest.NewRecorder()
		stationHandler.ServeHTTP(response, req)
		return response
	}
	catalogPath := "/api/control/station/v1/catalog"
	if got := stationRequest(catalogPath, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("plain HTTP returned %d", got)
	}
	if got := stationRequest(catalogPath, &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}).Code; got != http.StatusUnauthorized {
		t.Fatalf("unverified certificate returned %d", got)
	}
	other := &x509.Certificate{Raw: []byte("other certificate"), URIs: []*url.URL{uri}}
	if got := stationRequest(catalogPath, &tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}, VerifiedChains: [][]*x509.Certificate{{other}}}); got.Code != http.StatusOK || strings.Contains(got.Body.String(), appID) {
		t.Fatalf("ungranted certificate saw candidate: %d %s", got.Code, got.Body.String())
	}
	verified := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	listed := stationRequest(catalogPath, verified)
	if listed.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", listed.Code, listed.Body.String())
	}
	var catalog control.StationCandidateCatalog
	if err := json.Unmarshal(listed.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].Installable || catalog.Items[0].Status != "center_recorded" || catalog.Items[0].ManifestSHA256 != sealed.ManifestSHA256 {
		t.Fatalf("unsafe candidate catalog: %+v", catalog)
	}
	manifest := stationRequest(catalog.Items[0].ManifestPath, verified)
	if manifest.Code != http.StatusOK || manifest.Header().Get("X-Manifest-SHA256") != sealed.ManifestSHA256 || manifest.Body.String() != string(sealed.Manifest) {
		t.Fatalf("manifest mismatch: %d %s", manifest.Code, manifest.Body.String())
	}
	denied := stationRequest(catalog.Items[0].ManifestPath, &tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}, VerifiedChains: [][]*x509.Certificate{{other}}})
	if denied.Code != http.StatusNotFound {
		t.Fatalf("other certificate read manifest: %d", denied.Code)
	}
	revoked := request(t, ops, http.MethodDelete, grantPath+"/"+stationID, nil)
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoked.StatusCode, readBody(t, revoked))
	}
	revoked.Body.Close()
	if got := stationRequest(catalogPath, verified); got.Code != http.StatusOK || strings.Contains(got.Body.String(), appID) {
		t.Fatalf("revoked candidate remained visible: %d %s", got.Code, got.Body.String())
	}
	if got := stationRequest(catalog.Items[0].ManifestPath, verified).Code; got != http.StatusNotFound {
		t.Fatalf("revoked manifest remained readable: %d", got)
	}
}
