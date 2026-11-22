package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

const handoffProviderRef = "verdantflare-center"
const maxAgentHandoffBody = 16 << 10

type agentHandoffRepository interface {
	RegisterHandoff(context.Context, store.HandoffIntent, time.Time) (store.HandoffIntent, error)
	CancelHandoff(context.Context, string, string, string, string, string) error
	Handoff(context.Context, string) (store.HandoffIntent, error)
	HandoffByAuthentication(context.Context, string) (store.HandoffIntent, error)
	ConfirmHandoff(context.Context, string, string, string, string, store.HandoffLoginFact, time.Time) (store.HandoffIntent, error)
	ExchangeHandoff(context.Context, store.HandoffExchangeInput, string, string, string, store.HandoffLoginFact, time.Time) (store.HandoffProof, error)
	CheckHandoff(context.Context, string, string, string, string, string, string, string) (store.HandoffIdentity, error)
	ResolveHandoffIdentity(context.Context, string, string) (store.HandoffIdentity, error)
}

type agentHandoffLogin interface {
	Resolve(context.Context, string) (gateway.LoginHandoffFact, error)
	Check(context.Context, string, string) (gateway.LoginHandoffFact, error)
}

type AgentHandoffProvider struct {
	repository   agentHandoffRepository
	login        agentHandoffLogin
	registryFile string
	issuer       string
}

func NewAgentHandoffProvider(repository agentHandoffRepository, login agentHandoffLogin, registryFile, issuer string) (*AgentHandoffProvider, error) {
	if repository == nil || login == nil || registryFile == "" || issuer == "" {
		return nil, errors.New("agent handoff provider configuration incomplete")
	}
	if _, err := loadHandoffRegistry(registryFile); err != nil {
		return nil, err
	}
	return &AgentHandoffProvider{repository: repository, login: login, registryFile: registryFile, issuer: issuer}, nil
}

func handoffError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "HANDOFF_UNAVAILABLE"
	var problem *domain.Error
	if errors.As(err, &problem) {
		status, code = problem.Status, problem.Code
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}

func handoffJSON(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func handoffInvalid() error {
	return domain.NewError(400, "HANDOFF_INVALID_REQUEST", "请求格式无效")
}
func handoffForbidden() error {
	return domain.NewError(403, "HANDOFF_FORBIDDEN", "交接目标不匹配")
}
func handoffUnavailable() error {
	return domain.NewError(503, "HANDOFF_UNAVAILABLE", "身份状态暂不可验证")
}

// strictHandoffJSON rejects duplicate and unknown top-level fields; every v1
// service request is a flat JSON object, so no nested duplicate keys exist.
func strictHandoffJSON(w http.ResponseWriter, r *http.Request, target any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAgentHandoffBody))
	if err != nil || len(raw) == 0 {
		return handoffInvalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return handoffInvalid()
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return handoffInvalid()
		}
		key, ok := keyToken.(string)
		if !ok || fields[key] != nil {
			return handoffInvalid()
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return handoffInvalid()
		}
		fields[key] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return handoffInvalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return handoffInvalid()
	}
	canonical, _ := json.Marshal(fields)
	typed := json.NewDecoder(bytes.NewReader(canonical))
	typed.DisallowUnknownFields()
	if err := typed.Decode(target); err != nil {
		return handoffInvalid()
	}
	return nil
}

type handoffRegistration struct {
	ProtocolVersion int       `json:"protocol_version"`
	ProviderRef     string    `json:"provider_ref"`
	IntentID        string    `json:"intent_id"`
	RecipientRef    string    `json:"recipient_client_ref"`
	StationID       string    `json:"station_id"`
	LocalOrgRef     string    `json:"target_local_organization_ref"`
	ExternalOrgRef  string    `json:"expected_external_organization_ref"`
	CallbackRef     string    `json:"callback_ref"`
	Nonce           string    `json:"nonce"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type handoffExchangeRequest struct {
	ProtocolVersion int `json:"protocol_version"`
	store.HandoffExchangeInput
}

type handoffCancelRequest struct {
	ProtocolVersion int    `json:"protocol_version"`
	RequestRef      string `json:"request_ref"`
	IntentID        string `json:"intent_id"`
	RecipientRef    string `json:"recipient_client_ref"`
	StationID       string `json:"station_id"`
	Nonce           string `json:"nonce"`
}

type handoffStatusRequest struct {
	ProtocolVersion     int    `json:"protocol_version"`
	AuthenticationRef   string `json:"authentication_ref"`
	AuthenticationEpoch string `json:"authentication_epoch"`
	SubjectRef          string `json:"subject_ref"`
	CenterUserRef       string `json:"center_user_ref"`
	ExternalOrgRef      string `json:"external_organization_ref"`
	RecipientRef        string `json:"recipient_client_ref"`
	StationID           string `json:"station_id"`
}

type handoffBrowserViewRequest struct {
	RequestRef string `json:"request_ref"`
}

func handoffIdentifier(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 || r == '/' || r == '\\' || r == '?' || r == '#' {
			return false
		}
	}
	return true
}

func factForStore(fact gateway.LoginHandoffFact) store.HandoffLoginFact {
	return store.HandoffLoginFact{SubjectRef: fact.SubjectRef, AuthenticationRef: fact.AuthenticationRef, ExpiresAt: fact.LoginExpiresAt, CheckedAt: fact.CheckedAt}
}

// InternalHandler must be bound only to a separate TLS listener with verified
// client certificates. Registry and fingerprint are rechecked on each request.
func (p *AgentHandoffProvider) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/agent-handoff/v1/intents", p.register)
	mux.HandleFunc("POST /internal/agent-handoff/v1/intents/cancel", p.cancel)
	mux.HandleFunc("POST /internal/agent-handoff/v1/exchanges", p.exchange)
	mux.HandleFunc("POST /internal/agent-handoff/v1/authentications/check", p.status)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.PeerCertificates[0].URIs) != 1 {
			handoffError(w, domain.NewError(401, "HANDOFF_UNAUTHENTICATED", "需要专用服务证书"))
			return
		}
		registry, err := loadHandoffRegistry(p.registryFile)
		if err != nil {
			handoffError(w, handoffUnavailable())
			return
		}
		cert := r.TLS.PeerCertificates[0]
		var matched *handoffConsumer
		for i := range registry.Consumers {
			if handoffCertificateAllowed(registry.Consumers[i], cert.URIs[0].String(), cert.Raw) {
				matched = &registry.Consumers[i]
				break
			}
		}
		if matched == nil {
			handoffError(w, handoffForbidden())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(context.WithValue(ctx, handoffConsumerKey{}, *matched)))
	})
}

func (p *AgentHandoffProvider) cancel(w http.ResponseWriter, r *http.Request) {
	var input handoffCancelRequest
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	if input.ProtocolVersion != 1 || input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID || !handoffIdentifier(input.RequestRef) || !handoffIdentifier(input.IntentID) || len(input.Nonce) < 16 || len(input.Nonce) > 128 {
		handoffError(w, handoffInvalid())
		return
	}
	intent, err := p.repository.Handoff(r.Context(), input.RequestRef)
	if err != nil {
		handoffError(w, err)
		return
	}
	if err := p.currentConsumer(intent, consumer); err != nil {
		handoffError(w, err)
		return
	}
	if err := p.repository.CancelHandoff(r.Context(), input.RequestRef, input.IntentID, input.RecipientRef, input.StationID, input.Nonce); err != nil {
		handoffError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

type handoffConsumerKey struct{}

func (p *AgentHandoffProvider) register(w http.ResponseWriter, r *http.Request) {
	var input handoffRegistration
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	now := time.Now().UTC()
	if input.ProtocolVersion != 1 || input.ProviderRef != handoffProviderRef || input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID || !handoffIdentifier(input.IntentID) || !handoffIdentifier(input.LocalOrgRef) || !handoffIdentifier(input.ExternalOrgRef) || !handoffIdentifier(input.CallbackRef) || len(input.Nonce) < 16 || len(input.Nonce) > 128 || input.CreatedAt.IsZero() || input.ExpiresAt.IsZero() || input.CreatedAt.Before(now.Add(-5*time.Second)) || input.CreatedAt.After(now.Add(5*time.Second)) || !now.Before(input.ExpiresAt) || input.ExpiresAt.After(input.CreatedAt.Add(5*time.Minute)) {
		handoffError(w, handoffInvalid())
		return
	}
	registry, err := loadHandoffRegistry(p.registryFile)
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	current, route, ok := registry.route(input.RecipientRef, input.StationID, input.LocalOrgRef, input.ExternalOrgRef, input.CallbackRef)
	if !ok || current.ClientURI != consumer.ClientURI {
		handoffError(w, handoffForbidden())
		return
	}
	canonical, _ := json.Marshal(input)
	digest := sha256.Sum256(canonical)
	requestRef, err := store.NewHandoffRef()
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	intent := store.HandoffIntent{RequestRef: requestRef, RequestHash: hex.EncodeToString(digest[:]), ProviderRef: input.ProviderRef,
		IntentID: input.IntentID, RecipientRef: input.RecipientRef, StationID: input.StationID, LocalOrgRef: input.LocalOrgRef,
		ExternalOrgRef: input.ExternalOrgRef, CallbackRef: input.CallbackRef, CallbackURL: route.CallbackURL,
		BrowserOrigin: route.BrowserOrigin, ConfigRevision: registry.Version, Nonce: input.Nonce,
		CreatedAt: input.CreatedAt, ExpiresAt: input.ExpiresAt}
	result, err := p.repository.RegisterHandoff(r.Context(), intent, now)
	if err != nil {
		handoffError(w, err)
		return
	}
	browser, _ := url.Parse(result.BrowserOrigin)
	browser.Path = "/agent-handoff"
	browser.Fragment = "request_ref=" + url.QueryEscape(result.RequestRef)
	status := http.StatusCreated
	if result.RequestRef != requestRef {
		status = http.StatusOK
	}
	handoffJSON(w, status, map[string]any{"protocol_version": 1, "request_ref": result.RequestRef, "expires_at": result.ExpiresAt, "browser_url": browser.String()})
}

func (p *AgentHandoffProvider) currentConsumer(intent store.HandoffIntent, consumer handoffConsumer) error {
	registry, err := loadHandoffRegistry(p.registryFile)
	if err != nil {
		return handoffUnavailable()
	}
	current, route, ok := registry.route(intent.RecipientRef, intent.StationID, intent.LocalOrgRef, intent.ExternalOrgRef, intent.CallbackRef)
	if !ok || current.ClientURI != consumer.ClientURI || current.RecipientRef != consumer.RecipientRef || current.StationID != consumer.StationID || route.CallbackURL != intent.CallbackURL || route.BrowserOrigin != intent.BrowserOrigin {
		return handoffForbidden()
	}
	return nil
}

func (p *AgentHandoffProvider) exchange(w http.ResponseWriter, r *http.Request) {
	var input handoffExchangeRequest
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	if input.ProtocolVersion != 1 || input.ProviderRef != handoffProviderRef || input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID || !handoffIdentifier(input.RequestRef) || !handoffIdentifier(input.HandoffRef) || !handoffIdentifier(input.ExchangeID) {
		handoffError(w, handoffInvalid())
		return
	}
	intent, err := p.repository.Handoff(r.Context(), input.RequestRef)
	if err != nil {
		handoffError(w, err)
		return
	}
	if err := p.currentConsumer(intent, consumer); err != nil {
		handoffError(w, err)
		return
	}
	canonical, _ := json.Marshal(input)
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	eventID, err := store.NewHandoffRef()
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	loginFact, err := p.login.Check(r.Context(), intent.LoginSessionRef, intent.SubjectRef)
	if err != nil {
		handoffError(w, err)
		return
	}
	fact := factForStore(loginFact)
	proof, err := p.repository.ExchangeHandoff(r.Context(), input.HandoffExchangeInput, hash, eventID, p.issuer, fact, time.Now().UTC())
	if err != nil {
		handoffError(w, err)
		return
	}
	handoffJSON(w, http.StatusOK, proof)
}

func (p *AgentHandoffProvider) status(w http.ResponseWriter, r *http.Request) {
	var input handoffStatusRequest
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	if input.ProtocolVersion != 1 || input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID || !handoffIdentifier(input.AuthenticationRef) || !handoffIdentifier(input.AuthenticationEpoch) || !handoffIdentifier(input.SubjectRef) || !handoffIdentifier(input.CenterUserRef) || !handoffIdentifier(input.ExternalOrgRef) {
		handoffError(w, handoffInvalid())
		return
	}
	identity, err := p.repository.CheckHandoff(r.Context(), input.AuthenticationRef, input.AuthenticationEpoch, input.SubjectRef, input.CenterUserRef, input.ExternalOrgRef, input.RecipientRef, input.StationID)
	if err != nil {
		handoffError(w, err)
		return
	}
	loginIntent, err := p.repository.HandoffByAuthentication(r.Context(), input.AuthenticationRef)
	if err != nil {
		handoffError(w, err)
		return
	}
	if err := p.currentConsumer(loginIntent, consumer); err != nil {
		handoffError(w, err)
		return
	}
	loginFact, err := p.login.Check(r.Context(), loginIntent.LoginSessionRef, input.SubjectRef)
	if err != nil {
		handoffError(w, err)
		return
	}
	if loginFact.SubjectRef != identity.SubjectRef || loginFact.AuthenticationRef != loginIntent.LoginSessionRef {
		handoffError(w, handoffForbidden())
		return
	}
	handoffJSON(w, http.StatusOK, map[string]any{"protocol_version": 1, "active": true,
		"subject_ref": identity.SubjectRef, "center_user_ref": identity.CenterUserRef,
		"external_organization_ref": identity.ExternalOrgRef, "authentication_ref": input.AuthenticationRef,
		"authentication_epoch": input.AuthenticationEpoch, "identity_revision": identity.IdentityRevision,
		"checked_at": time.Now().UTC(), "login_expires_at": loginFact.LoginExpiresAt})
}

// BrowserIntentHandler is mounted by Hub's authenticated reverse proxy, but
// it independently resolves the real Login Cookie before any confirmation.
func (p *AgentHandoffProvider) BrowserIntentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	view := strings.HasSuffix(r.URL.Path, "/view")
	var ref, state string
	if view {
		var input handoffBrowserViewRequest
		if err := strictHandoffJSON(w, r, &input); err != nil {
			handoffError(w, err)
			return
		}
		ref = input.RequestRef
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, maxAgentHandoffBody)
		if err := r.ParseForm(); err != nil || len(r.PostForm["request_ref"]) != 1 || len(r.PostForm["state"]) != 1 || len(r.PostForm) != 2 {
			handoffError(w, handoffInvalid())
			return
		}
		ref, state = r.PostForm.Get("request_ref"), r.PostForm.Get("state")
	}
	if !handoffIdentifier(ref) {
		handoffError(w, handoffInvalid())
		return
	}
	intent, err := p.repository.Handoff(r.Context(), ref)
	if err != nil {
		handoffError(w, err)
		return
	}
	if !time.Now().Before(intent.ExpiresAt) {
		handoffError(w, domain.NewError(410, "HANDOFF_EXPIRED", "意图已过期"))
		return
	}
	if intent.Status == "cancelled" {
		handoffError(w, domain.NewError(410, "HANDOFF_EXPIRED", "意图已取消"))
		return
	}
	registry, err := loadHandoffRegistry(p.registryFile)
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	_, route, ok := registry.route(intent.RecipientRef, intent.StationID, intent.LocalOrgRef, intent.ExternalOrgRef, intent.CallbackRef)
	if !ok || route.BrowserOrigin != intent.BrowserOrigin || route.CallbackURL != intent.CallbackURL {
		handoffError(w, handoffForbidden())
		return
	}
	origin, _ := url.Parse(route.BrowserOrigin)
	if r.Host != origin.Host {
		handoffError(w, handoffForbidden())
		return
	}
	fact, err := p.login.Resolve(ctx, r.Header.Get("Cookie"))
	if err != nil {
		handoffError(w, err)
		return
	}
	if _, err := p.repository.ResolveHandoffIdentity(ctx, fact.SubjectRef, intent.ExternalOrgRef); err != nil {
		var problem *domain.Error
		if errors.As(err, &problem) && problem.Status == 503 {
			handoffError(w, handoffUnavailable())
		} else {
			handoffError(w, handoffForbidden())
		}
		return
	}
	if r.Header.Get("Origin") != route.BrowserOrigin {
		handoffError(w, handoffForbidden())
		return
	}
	if view {
		handoffJSON(w, http.StatusOK, map[string]any{"protocol_version": 1, "recipient_client_ref": intent.RecipientRef,
			"station_id": intent.StationID, "external_organization_ref": intent.ExternalOrgRef,
			"expires_at": intent.ExpiresAt, "status": intent.Status})
		return
	}
	if !handoffIdentifier(state) {
		handoffError(w, handoffInvalid())
		return
	}
	handoffRef, err := store.NewHandoffRef()
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	authRef, err := store.NewHandoffRef()
	if err != nil {
		handoffError(w, handoffUnavailable())
		return
	}
	confirmed, err := p.repository.ConfirmHandoff(ctx, ref, state, handoffRef, authRef, factForStore(fact), time.Now().UTC())
	if err != nil {
		handoffError(w, err)
		return
	}
	callback, _ := url.Parse(confirmed.CallbackURL)
	callback.Fragment = "handoff_ref=" + url.QueryEscape(confirmed.HandoffRef) + "&state=" + url.QueryEscape(confirmed.BrowserState)
	handoffJSON(w, http.StatusOK, map[string]string{"callback_url": callback.String()})
}
