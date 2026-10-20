package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type publishMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"params"`
}

func publicationMCPFailure(err error) (*domain.Error, bool) {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure, false
	}
	var syntax *json.SyntaxError
	var inputType *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &inputType) {
		return domain.NewError(http.StatusBadRequest, "INVALID_ARGUMENTS", "应用发布参数无效"), false
	}
	return domain.NewError(http.StatusServiceUnavailable, "PUBLISH_UNAVAILABLE", "应用发布服务暂时不可用，请回读状态后重试"), true
}

func (s *Server) studioPublishMCP(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.delegatedPublisher(w, r)
	if !ok {
		return
	}
	var request publishMCPRequest
	if !s.decode(w, r, &request) {
		return
	}
	if request.JSONRPC != "2.0" || len(request.ID) == 0 || request.Method != "tools/call" {
		s.writeError(w, r, domain.NewError(400, "MCP_INVALID", "应用发布只接受带请求 ID 的 tools/call"))
		return
	}
	var result any
	var err error
	switch request.Params.Name {
	case "app_publish.list":
		result, err = s.service.StudioPublicationDirectory(r.Context(), subject)
	case "app_publish.organizations":
		result, err = s.service.StudioPublicationOrganizations(r.Context(), subject)
	case "app_publish.get":
		var input struct {
			AppID string `json:"app_id"`
		}
		if err = json.Unmarshal(request.Params.Arguments, &input); err == nil {
			result, err = s.service.ManagedApp(r.Context(), subject, input.AppID)
		}
	case "app_publish.readiness":
		var input struct {
			AppID string `json:"app_id"`
		}
		if err = json.Unmarshal(request.Params.Arguments, &input); err == nil {
			result, err = s.service.AppPublicationReadiness(r.Context(), subject, input.AppID)
		}
	case "app_publish.publish":
		var input control.StudioPublicationInput
		if err = json.Unmarshal(request.Params.Arguments, &input); err == nil {
			result, err = s.service.PublishRegisteredOCI(r.Context(), subject, input)
		}
	default:
		err = domain.NewError(404, "TOOL_NOT_FOUND", "应用发布工具不存在")
	}
	if err != nil {
		failure, logInternal := publicationMCPFailure(err)
		if logInternal {
			s.logger.Error("studio publication failed", "error", err)
		}
		retryable := failure.Status == http.StatusTooManyRequests || failure.Status == http.StatusBadGateway || failure.Status == http.StatusServiceUnavailable || failure.Status == http.StatusGatewayTimeout
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"isError": true, "structuredContent": map[string]any{"status": failure.Status, "code": failure.Code, "message": failure.Message, "retryable": retryable}}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"structuredContent": result}})
}
