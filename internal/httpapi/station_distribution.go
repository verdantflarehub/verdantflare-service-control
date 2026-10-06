package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

var stationCertificateIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *Server) createStationTestGrant(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	var input control.CreateStationTestGrantInput
	if !s.decode(w, r, &input) {
		return
	}
	grant, err := s.service.CreateStationTestGrant(r.Context(), subject, r.PathValue("appID"), r.PathValue("version"), input)
	s.respond(w, r, grant, err, http.StatusCreated)
}

func (s *Server) listStationTestGrants(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	grants, err := s.service.ListStationTestGrants(r.Context(), subject, r.PathValue("appID"))
	s.respond(w, r, grants, err, http.StatusOK)
}

func (s *Server) revokeStationTestGrant(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.subject(w, r)
	if !ok {
		return
	}
	grant, err := s.service.RevokeStationTestGrant(r.Context(), subject, r.PathValue("appID"), r.PathValue("version"), r.PathValue("stationID"))
	s.respond(w, r, grant, err, http.StatusOK)
}

// StationHandler is intentionally not mounted on the browser-facing HTTP
// listener. The separate TLS listener requires a verified client certificate.
func (s *Server) StationHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/control/station/v1/catalog", s.stationCatalog)
	mux.HandleFunc("GET /api/control/station/v1/apps/{appID}/versions/{version}/manifest", s.stationManifest)
	return s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.stationIdentity(w, r); !ok {
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

func (s *Server) stationIdentity(w http.ResponseWriter, r *http.Request) (control.StationIdentity, bool) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		s.writeError(w, r, domain.NewError(401, "station_certificate_required", "需要受信任的 Station 设备证书"))
		return control.StationIdentity{}, false
	}
	cert := r.TLS.PeerCertificates[0]
	if len(cert.URIs) != 1 {
		s.writeError(w, r, domain.NewError(401, "station_identity_invalid", "Station 设备证书身份无效"))
		return control.StationIdentity{}, false
	}
	uri := cert.URIs[0]
	if uri.Scheme != "spiffe" || uri.Host != "verdantflarehub.com" || uri.User != nil || uri.Opaque != "" || uri.RawPath != "" || !strings.HasPrefix(uri.Path, "/station/") || uri.RawQuery != "" || uri.Fragment != "" {
		s.writeError(w, r, domain.NewError(401, "station_identity_invalid", "Station 设备证书身份无效"))
		return control.StationIdentity{}, false
	}
	stationID := strings.TrimPrefix(uri.Path, "/station/")
	if !stationCertificateIDPattern.MatchString(stationID) {
		s.writeError(w, r, domain.NewError(401, "station_identity_invalid", "Station 设备证书身份无效"))
		return control.StationIdentity{}, false
	}
	digest := sha256.Sum256(cert.Raw)
	return control.StationIdentity{StationID: stationID, CertificateSHA256: hex.EncodeToString(digest[:])}, true
}

func (s *Server) stationCatalog(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.stationIdentity(w, r)
	if !ok {
		return
	}
	result, err := s.service.StationCandidateCatalog(r.Context(), identity)
	s.respond(w, r, result, err, http.StatusOK)
}

func (s *Server) stationManifest(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.stationIdentity(w, r)
	if !ok {
		return
	}
	manifest, digest, err := s.service.StationCandidateManifest(r.Context(), identity, r.PathValue("appID"), r.PathValue("version"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Manifest-SHA256", digest)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(manifest)
}
