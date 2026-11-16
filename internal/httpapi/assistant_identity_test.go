package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type assistantRepositoryFixture struct {
	calls   int
	failure bool
}

func (f *assistantRepositoryFixture) ResolveAssistantIdentity(_ context.Context, subject string) (store.AssistantIdentity, error) {
	f.calls++
	if f.failure {
		return store.AssistantIdentity{}, errors.New("private database diagnostic")
	}
	return store.AssistantIdentity{UserID: subject, OrgID: "org-a", Active: true}, nil
}
func (f *assistantRepositoryFixture) CheckAssistantIdentity(ctx context.Context, user, org string) (store.AssistantIdentity, error) {
	return f.ResolveAssistantIdentity(ctx, user+org)
}

func TestAssistantHandlerRequiresVerifiedExactServiceIdentity(t *testing.T) {
	const service = "spiffe://verdantflare/assistant"
	fixture := &assistantRepositoryFixture{}
	handler, err := NewAssistantIdentityHandler(fixture, service)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse(service)
	other, _ := url.Parse("spiffe://verdantflare/station")
	for _, tc := range []struct {
		name  string
		state *tls.ConnectionState
		want  int
	}{
		{"plain", nil, 401},
		{"unverified", &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}, 401},
		{"wrong-service", &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{other}}}, VerifiedChains: [][]*x509.Certificate{{{}}}}, 403},
		{"verified", &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}, VerifiedChains: [][]*x509.Certificate{{{}}}}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/internal/assistant/identity/resolve", strings.NewReader(`{"login_subject":"login-a"}`))
			r.TLS = tc.state
			r.Header.Set("X-User-Id", "forged-admin")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, wanted %d", w.Code, tc.want)
			}
		})
	}
	if fixture.calls != 1 {
		t.Fatal("unauthenticated request reached repository")
	}
}

func TestAssistantHandlerRejectsAmbiguousBodyAndRedactsBackendErrors(t *testing.T) {
	const service = "spiffe://verdantflare/assistant"
	fixture := &assistantRepositoryFixture{}
	handler, _ := NewAssistantIdentityHandler(fixture, service)
	uri, _ := url.Parse(service)
	state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}, VerifiedChains: [][]*x509.Certificate{{{}}}}
	for _, body := range []string{`{}`, `{"login_subject":"a","org_id":"other"}`, `{"login_subject":"a","admin":true}`, `{"login_subject":"a"}{}`, `{"login_subject":"` + strings.Repeat("x", 17000) + `"}`} {
		r := httptest.NewRequest("POST", "/internal/assistant/identity/resolve", strings.NewReader(body))
		r.TLS = state
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid body returned %d", w.Code)
		}
	}
	fixture.failure = true
	r := httptest.NewRequest("POST", "/internal/assistant/identity/resolve", strings.NewReader(`{"login_subject":"a"}`))
	r.TLS = state
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("backend error was leaked")
	}
}
