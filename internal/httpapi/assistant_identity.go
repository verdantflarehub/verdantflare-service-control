package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type AssistantIdentityRepository interface {
	ResolveAssistantIdentity(context.Context, string) (store.AssistantIdentity, error)
	CheckAssistantIdentity(context.Context, string, string) (store.AssistantIdentity, error)
}

// NewAssistantIdentityHandler must only be mounted on the dedicated mTLS server,
// never on the public Control or Station listener. The middleware additionally
// rejects handlers accidentally called without a verified assistant certificate.
func NewAssistantIdentityHandler(repository AssistantIdentityRepository, clientURI string) (http.Handler, error) {
	uri, err := url.Parse(clientURI)
	if err != nil || uri.Scheme != "spiffe" || uri.Host == "" || uri.Path == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || repository == nil {
		return nil, errors.New("assistant service identity configuration is invalid")
	}
	mux := http.NewServeMux()
	handle := func(resolve bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Subject string `json:"login_subject"`
				UserID  string `json:"center_user_id"`
				OrgID   string `json:"org_id"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				http.Error(w, "invalid identity request", 400)
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				http.Error(w, "invalid identity request", 400)
				return
			}
			if (resolve && (input.Subject == "" || input.UserID != "" || input.OrgID != "")) || (!resolve && (input.Subject != "" || input.UserID == "" || input.OrgID == "")) {
				http.Error(w, "invalid identity request", 400)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			defer cancel()
			var result store.AssistantIdentity
			var err error
			if resolve {
				result, err = repository.ResolveAssistantIdentity(ctx, input.Subject)
			} else {
				result, err = repository.CheckAssistantIdentity(ctx, input.UserID, input.OrgID)
			}
			if err != nil {
				var problem *domain.Error
				if errors.As(err, &problem) && problem.Status == 403 {
					http.Error(w, "IDENTITY_DENIED", 403)
				} else {
					http.Error(w, "IDENTITY_UNAVAILABLE", 503)
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		}
	}
	mux.HandleFunc("POST /internal/assistant/identity/resolve", handle(true))
	mux.HandleFunc("POST /internal/assistant/identity/check", handle(false))
	registerAssistantResourceRoutes(mux, repository)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "assistant mTLS required", 401)
			return
		}
		match := false
		for _, identity := range r.TLS.PeerCertificates[0].URIs {
			if identity.String() == clientURI {
				match = true
			}
		}
		if !match {
			http.Error(w, "assistant service identity denied", 403)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}
