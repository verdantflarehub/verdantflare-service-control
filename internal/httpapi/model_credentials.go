package httpapi

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// uploadModelCredential is the sole path for supplier key bytes. It does not
// accept JSON-RPC arguments and never returns or logs the supplied credential.
func (s *Server) uploadModelCredential(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		s.writeError(w, r, domain.NewError(403, "MODEL_CREDENTIAL_TLS_REQUIRED", "凭据上传需要 HTTPS"))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "https" || parsed.Host != r.Host {
			s.writeError(w, r, domain.NewError(403, "MODEL_CREDENTIAL_ORIGIN_FORBIDDEN", "凭据上传来源无效"))
			return
		}
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		s.writeError(w, r, domain.NewError(415, "MODEL_CREDENTIAL_CONTENT_TYPE", "凭据上传须使用二进制内容类型"))
		return
	}
	secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || strings.TrimSpace(secret) == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hub-mcp"`)
		s.writeError(w, r, domain.NewError(401, "mcp_token_required", "缺少 Hub MCP 访问令牌"))
		return
	}
	identity, err := s.service.AuthenticateMCPToken(r.Context(), strings.TrimSpace(secret))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	bytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if err != nil {
		s.writeError(w, r, domain.NewError(413, "MODEL_CREDENTIAL_TOO_LARGE", "上游凭据超过允许大小"))
		return
	}
	defer func() {
		for i := range bytes {
			bytes[i] = 0
		}
	}()
	result, err := s.service.UploadModelCredential(r.Context(), identity, r.PathValue("handle"), string(bytes))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
