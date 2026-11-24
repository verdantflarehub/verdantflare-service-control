package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type directoryRepositoryStub struct {
	*store.Memory
	revision string
	checks   int
}

const directoryTestSubject = "00000000-0000-4000-8000-000000000099"

func (r *directoryRepositoryStub) DirectoryRevision(context.Context) (string, time.Time, error) {
	return r.revision, time.Now().UTC(), nil
}
func (r *directoryRepositoryStub) CheckDirectoryMember(_ context.Context, subject, org, expected string) (store.HandoffIdentity, error) {
	r.checks++
	if expected != r.revision {
		return store.HandoffIdentity{}, domain.NewError(409, "HANDOFF_REVISION_CHANGED", "changed")
	}
	if subject != directoryTestSubject || org != "org-1" {
		return store.HandoffIdentity{}, domain.NewError(403, "IDENTITY_DENIED", "denied")
	}
	return store.HandoffIdentity{SubjectRef: subject, CenterUserRef: "center-1", ExternalOrgRef: org, IdentityRevision: r.revision, CheckedAt: time.Now().UTC()}, nil
}

type directoryLoginStub struct {
	afterCheck func()
	failure    error
}

func (directoryLoginStub) Resolve(context.Context, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{}, nil
}
func (directoryLoginStub) Check(context.Context, string, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{}, nil
}
func (s directoryLoginStub) CheckSubject(_ context.Context, subject string) (gateway.LoginSubjectFact, error) {
	if s.failure != nil {
		return gateway.LoginSubjectFact{}, s.failure
	}
	if s.afterCheck != nil {
		s.afterCheck()
	}
	return gateway.LoginSubjectFact{SubjectRef: subject, Active: true, CheckedAt: time.Now().UTC()}, nil
}

func TestHandoffDirectoryRequiresExactRouteAndRevision(t *testing.T) {
	const clientURI = "spiffe://verdantflare.test/agent-handoff/station-1"
	leafURI, _ := url.Parse(clientURI)
	cert := &x509.Certificate{Raw: []byte("directory-test-cert"), URIs: []*url.URL{leafURI}}
	digest := sha256.Sum256(cert.Raw)
	registry := handoffRegistry{Version: "test-v1", Consumers: []handoffConsumer{{
		ClientURI: clientURI, CertificateSHA256: []string{hex.EncodeToString(digest[:])},
		RecipientRef: "studio-service", StationID: "station-1", Enabled: true,
		Routes: []handoffRoute{{LocalOrgRef: "local-1", ExternalOrgRef: "org-1", CallbackRef: "studio",
			CallbackURL: "https://studio.example.test/identity/callback", BrowserOrigin: "https://hub.example.test"}},
	}}}
	registryFile := filepath.Join(t.TempDir(), "registry.json")
	writeRegistry := func() {
		raw, err := json.Marshal(registry)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(registryFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRegistry()
	repository := &directoryRepositoryStub{Memory: store.NewMemoryBootstrap(), revision: "7"}
	login := directoryLoginStub{}
	provider, err := NewAgentHandoffProvider(repository, login, registryFile, "https://hub.example.test")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}
		w := httptest.NewRecorder()
		provider.InternalHandler().ServeHTTP(w, r)
		return w
	}
	revisionPath := "/internal/agent-handoff/v1/directory/revision"
	memberPath := "/internal/agent-handoff/v1/directory/members/check"
	revisionBody := `{"protocol_version":1,"provider_ref":"verdantflare-center","recipient_client_ref":"studio-service","station_id":"station-1"}`
	if got := request(revisionPath, revisionBody); got.Code != 200 || !strings.Contains(got.Body.String(), `"identity_revision":"7"`) {
		t.Fatalf("revision: %d %s", got.Code, got.Body.String())
	}
	memberBody := `{"protocol_version":1,"provider_ref":"verdantflare-center","recipient_client_ref":"studio-service","station_id":"station-1","target_local_organization_ref":"local-1","external_organization_ref":"org-1","subject_ref":"` + directoryTestSubject + `","expected_identity_revision":"7"}`
	if got := request(memberPath, memberBody); got.Code != 200 || !strings.Contains(got.Body.String(), `"center_user_ref":"center-1"`) || strings.Contains(got.Body.String(), "email") || repository.checks != 2 {
		t.Fatalf("member fact: %d %s checks=%d", got.Code, got.Body.String(), repository.checks)
	}
	for body, want := range map[string]int{
		strings.Replace(memberBody, `"local-1"`, `"other-local"`, 1):                                           403,
		strings.Replace(memberBody, `"org-1"`, `"other-org"`, 1):                                               403,
		strings.Replace(memberBody, `"station-1"`, `"station-2"`, 1):                                           403,
		strings.Replace(memberBody, `"`+directoryTestSubject+`"`, `"00000000-0000-4000-8000-000000000077"`, 1): 403,
		strings.Replace(memberBody, `"expected_identity_revision":"7"`, `"expected_identity_revision":"6"`, 1): 409,
		memberBody + `{}`: 400,
	} {
		if got := request(memberPath, body); got.Code != want {
			t.Fatalf("wrong scope accepted: got %d want %d: %s", got.Code, want, got.Body.String())
		}
	}
	provider.login = directoryLoginStub{failure: domain.NewError(503, "HANDOFF_UNAVAILABLE", "Login offline")}
	if got := request(memberPath, memberBody); got.Code != 503 {
		t.Fatalf("Login failure not closed: %d %s", got.Code, got.Body.String())
	}
	provider.login = directoryLoginStub{failure: domain.NewError(403, "HANDOFF_FORBIDDEN", "account disabled")}
	if got := request(memberPath, memberBody); got.Code != 403 {
		t.Fatalf("disabled Login subject: %d %s", got.Code, got.Body.String())
	}
	login.afterCheck = func() { repository.revision = "8" }
	provider.login = login
	if got := request(memberPath, memberBody); got.Code != 409 {
		t.Fatalf("mid-check revision race: %d %s", got.Code, got.Body.String())
	}
	repository.revision = "7"
	login.afterCheck = func() { registry.Consumers[0].Enabled = false; writeRegistry() }
	provider.login = login
	if got := request(memberPath, memberBody); got.Code != 403 {
		t.Fatalf("mid-check registration revoke: %d %s", got.Code, got.Body.String())
	}
}

func TestHandoffDirectoryRateIsBoundedPerRegisteredConsumer(t *testing.T) {
	provider := &AgentHandoffProvider{directoryUse: map[string]directoryRateWindow{}}
	consumer := handoffConsumer{RecipientRef: "studio-service", StationID: "station-1"}
	for i := 0; i < directoryRequestsPerSecond; i++ {
		if !provider.directoryRateAllowed(consumer) {
			t.Fatalf("request %d refused early", i)
		}
	}
	if provider.directoryRateAllowed(consumer) {
		t.Fatal("unbounded directory queries")
	}
	if !provider.directoryRateAllowed(handoffConsumer{RecipientRef: "other-service", StationID: "station-2"}) {
		t.Fatal("one consumer exhausted another consumer")
	}
}

func TestHandoffDirectoryRejectsAmbiguousLocalOrganization(t *testing.T) {
	registry := handoffRegistry{Version: "test-v1", Consumers: []handoffConsumer{{
		ClientURI: "spiffe://verdantflare.test/station-1", CertificateSHA256: []string{strings.Repeat("a", 64)},
		RecipientRef: "studio-service", StationID: "station-1", Enabled: true,
		Routes: []handoffRoute{
			{LocalOrgRef: "local-1", ExternalOrgRef: "org-1", CallbackRef: "first", CallbackURL: "https://studio.example.test/first", BrowserOrigin: "https://hub.example.test"},
			{LocalOrgRef: "local-1", ExternalOrgRef: "org-2", CallbackRef: "second", CallbackURL: "https://studio.example.test/second", BrowserOrigin: "https://hub.example.test"},
		},
	}}}
	path := filepath.Join(t.TempDir(), "registry.json")
	raw, _ := json.Marshal(registry)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRegistry(path); err == nil {
		t.Fatal("ambiguous local-to-external mapping accepted")
	}
	registry.Consumers[0].Routes[1].ExternalOrgRef = "org-1"
	raw, _ = json.Marshal(registry)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRegistry(path); err != nil {
		t.Fatalf("same organization with distinct callbacks rejected: %v", err)
	}
}
