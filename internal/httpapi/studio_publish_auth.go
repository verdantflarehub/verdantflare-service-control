package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// delegatedPublisher accepts only a short-lived request from the Studio
// publishing service. The bearer used at Studio /mcp never reaches Control.
func (s *Server) delegatedPublisher(w http.ResponseWriter, r *http.Request) (string, bool) {
	if len(s.config.StudioPublishHMACKey) < 32 || len(s.config.StudioPublishBindings) == 0 {
		s.writeError(w, r, domain.NewError(503, "STUDIO_PUBLISH_UNAVAILABLE", "Studio 发布身份绑定未配置"))
		return "", false
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		s.writeError(w, r, domain.NewError(400, "INVALID_REQUEST", "请求路径或方法无效"))
		return "", false
	}
	userID := r.Header.Get("X-App-Publish-User")
	orgID := r.Header.Get("X-App-Publish-Organization")
	timestamp := r.Header.Get("X-App-Publish-Timestamp")
	nonce := r.Header.Get("X-App-Publish-Nonce")
	signature := r.Header.Get("X-App-Publish-Signature")
	for _, key := range []string{"X-App-Publish-User", "X-App-Publish-Organization", "X-App-Publish-Timestamp", "X-App-Publish-Nonce", "X-App-Publish-Signature"} {
		if len(r.Header.Values(key)) != 1 {
			s.writeError(w, r, domain.NewError(401, "DELEGATION_INVALID", "发布委托无效"))
			return "", false
		}
	}
	issued, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(nonce) < 16 || len(nonce) > 64 || strings.ContainsAny(nonce, " \t\r\n/") || time.Since(time.Unix(issued, 0)) > 60*time.Second || time.Until(time.Unix(issued, 0)) > 10*time.Second {
		s.writeError(w, r, domain.NewError(401, "DELEGATION_EXPIRED", "发布委托已失效"))
		return "", false
	}
	var bindingSubject, boundOrg string
	for _, binding := range s.config.StudioPublishBindings {
		if binding.StationUserID == userID && binding.StationOrganizationID == orgID {
			bindingSubject, boundOrg = binding.LoginSubject, binding.ControlOrganizationID
			break
		}
	}
	if bindingSubject == "" {
		s.writeError(w, r, domain.NewError(403, "PUBLISHER_NOT_BOUND", "当前 Studio 身份未绑定发布者"))
		return "", false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, r, domain.NewError(400, "INVALID_REQUEST", "发布请求体过大"))
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	bodyDigest := sha256.Sum256(body)
	message := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%x", r.Method, r.URL.Path, timestamp, nonce, userID, orgID, bodyDigest)
	mac := hmac.New(sha256.New, []byte(s.config.StudioPublishHMACKey))
	_, _ = mac.Write([]byte(message))
	provided, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		s.writeError(w, r, domain.NewError(401, "DELEGATION_INVALID", "发布委托无效"))
		return "", false
	}
	s.publishNonceMu.Lock()
	for value, expires := range s.publishNonces {
		if time.Now().After(expires) {
			delete(s.publishNonces, value)
		}
	}
	key := userID + "/" + nonce
	if _, repeated := s.publishNonces[key]; repeated {
		s.publishNonceMu.Unlock()
		s.writeError(w, r, domain.NewError(409, "DELEGATION_REPLAY", "发布请求已处理，请回读结果"))
		return "", false
	}
	s.publishNonces[key] = time.Now().Add(70 * time.Second)
	s.publishNonceMu.Unlock()
	if err := s.service.AssertDelegatedPublisher(r.Context(), bindingSubject, boundOrg); err != nil {
		s.writeError(w, r, err)
		return "", false
	}
	return bindingSubject, true
}
