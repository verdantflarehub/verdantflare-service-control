package httpapi

import (
	"encoding/json"
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
		if _, ok := err.(*domain.Error); !ok {
			err = domain.NewError(400, "INVALID_ARGUMENTS", "应用发布参数无效")
		}
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"isError": true, "structuredContent": err}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"structuredContent": result}})
}
