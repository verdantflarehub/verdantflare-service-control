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

	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type handoffLoginStub struct{}

func (handoffLoginStub) Resolve(context.Context, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{}, nil
}
func (handoffLoginStub) Check(context.Context, string, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{}, nil
}

type browserHandoffLoginStub struct{}

func (browserHandoffLoginStub) Resolve(context.Context, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{SubjectRef: "subject-1", AuthenticationRef: "session-1", LoginExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (browserHandoffLoginStub) Check(context.Context, string, string) (gateway.LoginHandoffFact, error) {
	return gateway.LoginHandoffFact{}, nil
}

type browserHandoffRepository struct {
	*store.Memory
	confirmed bool
}

func (r *browserHandoffRepository) ResolveHandoffIdentity(context.Context, string, string) (store.HandoffIdentity, error) {
	return store.HandoffIdentity{SubjectRef: "subject-1", CenterUserRef: "center-1", ExternalOrgRef: "org-1"}, nil
}
func (r *browserHandoffRepository) ConfirmHandoff(_ context.Context, ref, state, handoffRef, _ string, _ store.HandoffLoginFact, _ time.Time) (store.HandoffIntent, error) {
	r.confirmed = true
	return store.HandoffIntent{RequestRef: ref, BrowserState: state, HandoffRef: handoffRef, CallbackURL: "https://studio.example.test/identity/callback"}, nil
}

func TestAgentHandoffRegistrationRequiresExactCertificateAndStrictBody(t *testing.T) {
	const clientURI = "spiffe://verdantflare.test/agent-handoff/station-1"
	leafURI, _ := url.Parse(clientURI)
	cert := &x509.Certificate{Raw: []byte("test-leaf-certificate"), URIs: []*url.URL{leafURI}}
	digest := sha256.Sum256(cert.Raw)
	registry := handoffRegistry{Version: "test-v1", Consumers: []handoffConsumer{{
		ClientURI: clientURI, CertificateSHA256: []string{hex.EncodeToString(digest[:])},
		RecipientRef: "studio-service", StationID: "station-1", Enabled: true,
		Routes: []handoffRoute{{LocalOrgRef: "local-1", ExternalOrgRef: "org-1", CallbackRef: "studio",
			CallbackURL: "https://studio.example.test/identity/callback", BrowserOrigin: "https://hub.example.test"}},
	}}}
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	registryFile := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registryFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewAgentHandoffProvider(store.NewMemoryBootstrap(), handoffLoginStub{}, registryFile, "https://hub.example.test")
	if err != nil {
		t.Fatal(err)
	}
	handler := provider.InternalHandler()
	now := time.Now().UTC()
	registration := handoffRegistration{ProtocolVersion: 1, ProviderRef: handoffProviderRef, IntentID: "intent-1",
		RecipientRef: "studio-service", StationID: "station-1", LocalOrgRef: "local-1", ExternalOrgRef: "org-1",
		CallbackRef: "studio", Nonce: strings.Repeat("n", 32), CreatedAt: now, ExpiresAt: now.Add(4 * time.Minute)}
	body, _ := json.Marshal(registration)
	request := func(payload string, withTLS bool, uri *url.URL) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/internal/agent-handoff/v1/intents", strings.NewReader(payload))
		if withTLS {
			other := *cert
			other.URIs = []*url.URL{uri}
			r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{&other}}, PeerCertificates: []*x509.Certificate{&other}}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(string(body), false, leafURI); got.Code != 401 {
		t.Fatalf("missing mTLS: %d", got.Code)
	}
	wrongURI, _ := url.Parse("spiffe://verdantflare.test/assistant")
	if got := request(string(body), true, wrongURI); got.Code != 403 {
		t.Fatalf("wrong service identity: %d", got.Code)
	}
	if got := request(`{"protocol_version":1,"protocol_version":1}`, true, leafURI); got.Code != 400 {
		t.Fatalf("duplicate field: %d", got.Code)
	}
	accepted := request(string(body), true, leafURI)
	if accepted.Code != 201 || strings.Contains(accepted.Body.String(), registration.Nonce) {
		t.Fatalf("registered intent: %d %s", accepted.Code, accepted.Body.String())
	}
	var result struct {
		BrowserURL string `json:"browser_url"`
		RequestRef string `json:"request_ref"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &result); err != nil || result.RequestRef == "" || !strings.HasPrefix(result.BrowserURL, "https://hub.example.test/agent-handoff#request_ref=") {
		t.Fatalf("fixed browser route: %+v %v", result, err)
	}
	again := request(string(body), true, leafURI)
	if again.Code != 200 {
		t.Fatalf("idempotent registration: %d", again.Code)
	}
	cancelBody, _ := json.Marshal(handoffCancelRequest{ProtocolVersion: 1, RequestRef: result.RequestRef,
		IntentID: registration.IntentID, RecipientRef: registration.RecipientRef, StationID: registration.StationID, Nonce: registration.Nonce})
	cancelRequest := func(payload string, uri *url.URL) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/internal/agent-handoff/v1/intents/cancel", strings.NewReader(payload))
		other := *cert
		other.URIs = []*url.URL{uri}
		r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{&other}}, PeerCertificates: []*x509.Certificate{&other}}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := cancelRequest(string(cancelBody), wrongURI); got.Code != 403 {
		t.Fatalf("wrong service cancelled intent: %d", got.Code)
	}
	if got := cancelRequest(string(cancelBody), leafURI); got.Code != 204 {
		t.Fatalf("cancel failed: %d %s", got.Code, got.Body.String())
	}
	if got := cancelRequest(string(cancelBody), leafURI); got.Code != 204 {
		t.Fatalf("cancel retry failed: %d", got.Code)
	}
	if err := os.WriteFile(registryFile, []byte(`{"version":"test-v2","consumers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := request(string(body), true, leafURI); got.Code != 503 {
		t.Fatalf("unavailable registry failed open: %d", got.Code)
	}
}

func TestHandoffRegistryFailsClosedOnSizeAndSharedIdentity(t *testing.T) {
	const base = `{"version":"test","consumers":[{"client_uri":"spiffe://vf.test/station-1","certificate_sha256":["%s"],"recipient_client_ref":"studio","station_id":"station-1","enabled":true,"routes":[{"target_local_organization_ref":"local","external_organization_ref":"org","callback_ref":"callback","callback_url":"https://studio.example.test/callback","browser_origin":"https://hub.example.test"}]}]}`
	digest := strings.Repeat("a", 64)
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(strings.Replace(base, "%s", digest, 1)+strings.Repeat(" ", 64<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRegistry(path); err == nil {
		t.Fatal("oversized registry accepted")
	}
	var registry handoffRegistry
	if err := json.Unmarshal([]byte(strings.Replace(base, "%s", digest, 1)), &registry); err != nil {
		t.Fatal(err)
	}
	second := registry.Consumers[0]
	second.ClientURI = "spiffe://vf.test/station-2"
	second.StationID = "station-2"
	registry.Consumers = append(registry.Consumers, second)
	shared, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, shared, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRegistry(path); err == nil {
		t.Fatal("shared certificate accepted across consumers")
	}
}

func TestBrowserHandoffKeepsRefsOutOfRequestAndCallbackURLs(t *testing.T) {
	registry := handoffRegistry{Version: "test-v1", Consumers: []handoffConsumer{{
		ClientURI: "spiffe://vf.test/station-1", CertificateSHA256: []string{strings.Repeat("a", 64)},
		RecipientRef: "studio-service", StationID: "station-1", Enabled: true,
		Routes: []handoffRoute{{LocalOrgRef: "local-1", ExternalOrgRef: "org-1", CallbackRef: "studio",
			CallbackURL: "https://studio.example.test/identity/callback", BrowserOrigin: "https://hub.example.test"}},
	}}}
	raw, _ := json.Marshal(registry)
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	repository := &browserHandoffRepository{Memory: store.NewMemoryBootstrap()}
	intent := store.HandoffIntent{RequestRef: "request-123", RequestHash: "hash-123", IntentID: "intent-123",
		RecipientRef: "studio-service", StationID: "station-1", LocalOrgRef: "local-1", ExternalOrgRef: "org-1",
		CallbackRef: "studio", CallbackURL: "https://studio.example.test/identity/callback", BrowserOrigin: "https://hub.example.test",
		Nonce: "secret-nonce-1234", ExpiresAt: time.Now().Add(5 * time.Minute)}
	if _, err := repository.RegisterHandoff(context.Background(), intent, time.Now()); err != nil {
		t.Fatal(err)
	}
	provider, err := NewAgentHandoffProvider(repository, browserHandoffLoginStub{}, path, "https://hub.example.test")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body, contentType, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://hub.example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", origin)
		r.Header.Set("Cookie", "vf_session=test-only-cookie")
		w := httptest.NewRecorder()
		provider.BrowserIntentHandler(w, r)
		return w
	}
	view := request("/api/control/agent-handoff/v1/intents/view", `{"request_ref":"request-123"}`, "application/json", "https://hub.example.test")
	if view.Code != 200 || strings.Contains(view.Body.String(), intent.Nonce) || strings.Contains(view.Body.String(), "session-1") {
		t.Fatalf("unsafe view: %d %s", view.Code, view.Body.String())
	}
	if got := request("/api/control/agent-handoff/v1/intents/view", `{"request_ref":"request-123"}`, "application/json", "https://attacker.example.test"); got.Code != 403 {
		t.Fatalf("cross-origin view: %d", got.Code)
	}
	confirm := request("/api/control/agent-handoff/v1/intents/confirm", "request_ref=request-123&state=state-123", "application/x-www-form-urlencoded", "https://hub.example.test")
	var callback struct {
		CallbackURL string `json:"callback_url"`
	}
	if err := json.Unmarshal(confirm.Body.Bytes(), &callback); err != nil || confirm.Code != 200 || !repository.confirmed || strings.Contains(callback.CallbackURL, "?") || !strings.Contains(callback.CallbackURL, "#handoff_ref=") || !strings.Contains(callback.CallbackURL, "&state=state-123") {
		t.Fatalf("unsafe callback: %d %s", confirm.Code, confirm.Body.String())
	}
}
