package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type hubMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type hubMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func decodeModelMCPWriteArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.NewError(400, "MODEL_ARGUMENTS_INVALID", "模型工具参数无效或包含不支持字段")
	}
	return nil
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

func hubMCPFailure(err error, purpose string) (*domain.Error, bool) {
	if purpose != control.MCPTokenPurposeModel {
		return publicationMCPFailure(err)
	}
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure, false
	}
	var syntax *json.SyntaxError
	var inputType *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &inputType) {
		return domain.NewError(http.StatusBadRequest, "MODEL_ARGUMENTS_INVALID", "模型工具参数无效"), false
	}
	return domain.NewError(http.StatusServiceUnavailable, "MODEL_CONFIG_UNAVAILABLE", "模型配置回读暂时不可用，请稍后重试"), true
}

// hubMCP serves the Hub Market MCP. The bearer is a Hub MCP token; Login
// session headers and the development subject are never consulted here.
func (s *Server) hubMCP(w http.ResponseWriter, r *http.Request) {
	secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || strings.TrimSpace(secret) == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hub-mcp"`)
		s.writeError(w, r, domain.NewError(401, "mcp_token_required", "缺少 Hub MCP 访问令牌"))
		return
	}
	identity, err := s.service.AuthenticateMCPToken(r.Context(), strings.TrimSpace(secret))
	if err != nil {
		var failure *domain.Error
		if errors.As(err, &failure) && failure.Status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hub-mcp", error="invalid_token"`)
		}
		s.writeError(w, r, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPublicationBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, r, domain.NewError(413, "REQUEST_TOO_LARGE", "MCP 请求体超过 5 MiB"))
		} else {
			s.writeError(w, r, domain.NewError(400, "INVALID_REQUEST", "MCP 请求体读取失败"))
		}
		return
	}
	var request hubMCPRequest
	if err := json.Unmarshal(body, &request); err != nil || request.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "无效的 JSON-RPC 请求"}})
		return
	}
	if len(request.ID) == 0 {
		// Notifications such as notifications/initialized need no reply.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	reply := func(result any) {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.ProtocolVersion == "" {
			params.ProtocolVersion = "2025-03-26"
		}
		reply(map[string]any{"protocolVersion": params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "verdantflare-hub-market", "version": "1"}})
	case "ping":
		reply(map[string]any{})
	case "tools/list":
		reply(map[string]any{"tools": hubMCPTools(identity.Purpose)})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32602, "message": "tools/call 参数无效"}})
			return
		}
		if len(params.Arguments) == 0 {
			params.Arguments = json.RawMessage("{}")
		}
		result, err := s.callHubMCPTool(r, identity, params.Name, params.Arguments)
		if err != nil {
			failure, logInternal := hubMCPFailure(err, identity.Purpose)
			if logInternal {
				s.logger.Error("hub mcp tool failed", "request_id", requestID(r.Context()), "tool", params.Name, "error", err)
			}
			retryable := failure.Status == http.StatusTooManyRequests || failure.Status == http.StatusBadGateway || failure.Status == http.StatusServiceUnavailable || failure.Status == http.StatusGatewayTimeout
			content := map[string]any{"status": failure.Status, "code": failure.Code, "message": failure.Message, "retryable": retryable}
			reply(map[string]any{"isError": true, "structuredContent": content, "content": mcpTextContent(content)})
			return
		}
		s.logger.Info("hub mcp tool", "request_id", requestID(r.Context()), "tool", params.Name, "token_id", identity.TokenID)
		reply(map[string]any{"structuredContent": result, "content": mcpTextContent(result)})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "不支持的方法"}})
	}
}

func mcpTextContent(value any) []map[string]any {
	text, _ := json.Marshal(value)
	return []map[string]any{{"type": "text", "text": string(text)}}
}

func (s *Server) callHubMCPTool(r *http.Request, identity control.MCPIdentity, name string, arguments json.RawMessage) (any, error) {
	subject, purpose := identity.Subject, identity.Purpose
	if purpose == control.MCPTokenPurposeModel && strings.HasPrefix(name, "model_publish.") {
		switch name {
		case "model_publish.publish":
			var input control.PublishModelInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.PublishModel(r.Context(), subject, input)
		case "model_publish.publish.status":
			var input struct {
				ModelID string `json:"modelId"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.ModelPublicationStatus(r.Context(), subject, input.ModelID)
		case "model_publish.publish.resume":
			var input struct {
				ModelID   string `json:"modelId"`
				RequestID string `json:"requestId"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.ResumeModelPublication(r.Context(), subject, input.ModelID, input.RequestID)
		case "model_publish.credentials.prepare":
			var input control.PrepareModelCredentialInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.PrepareModelCredential(r.Context(), identity, input)
		case "model_publish.credentials.rotate":
			var input control.PrepareModelCredentialInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.PrepareModelCredentialRotation(r.Context(), identity, input)
		case "model_publish.credentials.status":
			var input struct {
				Handle string `json:"handle"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.ModelCredentialStatus(r.Context(), identity, input.Handle)
		case "model_publish.group.preview":
			return s.service.GatewayGroupPreview(r.Context(), subject)
		case "model_publish.group.apply":
			var input gateway.ModelOpsGroupApplyInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayGroupApply(r.Context(), subject, input)
		case "model_publish.gateway_model.configure":
			var input gateway.ModelOpsMetadataInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayConfigureMetadata(r.Context(), subject, input)
		case "model_publish.billing.apply":
			var input gateway.ModelOpsBillingInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayBillingApply(r.Context(), subject, input)
		case "model_publish.channel.attach_alias":
			var input gateway.ModelOpsAliasInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayAttachAlias(r.Context(), subject, input)
		case "model_publish.channel.disable":
			var input gateway.ModelOpsChannelDisableInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayDisableChannel(r.Context(), subject, input)
		case "model_publish.channel.update":
			var input gateway.ModelOpsChannelUpdateInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayUpdateChannel(r.Context(), subject, input)
		case "model_publish.probe":
			var input gateway.ModelOpsProbeInput
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayModelProbe(r.Context(), subject, input)
		case "model_publish.probe.status":
			var input struct {
				ModelID                 string `json:"modelId"`
				RequestID               string `json:"requestId"`
				ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayModelProbeStatus(r.Context(), subject, input.ModelID, input.RequestID, input.ExpectedGatewayRevision)
		case "model_publish.draft.save":
			var input struct {
				Model            domain.PublicModel `json:"model"`
				ExpectedRevision int                `json:"expectedRevision"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.SaveModelDraft(r.Context(), subject, input.Model, input.ExpectedRevision)
		case "model_publish.unpublish":
			var input struct {
				ModelID          string `json:"modelId"`
				ExpectedRevision int    `json:"expectedRevision"`
			}
			if err := decodeModelMCPWriteArguments(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.UnpublishModel(r.Context(), subject, input.ModelID, input.ExpectedRevision)
		case "model_publish.list":
			return s.service.ListManagedModels(r.Context(), subject)
		case "model_publish.gateway_models":
			return s.service.ListGatewayModels(r.Context(), subject)
		case "model_publish.gateway_snapshot":
			var input struct {
				ModelID string `json:"modelId"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayModelSnapshot(r.Context(), subject, input.ModelID)
		case "model_publish.billing.preview":
			var input gateway.ModelOpsBillingInput
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.GatewayBillingPreview(r.Context(), subject, input)
		case "model_publish.validate":
			var input struct {
				ModelID string `json:"modelId"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			return s.service.ValidateModelPublication(r.Context(), subject, input.ModelID)
		case "model_publish.get":
			var input struct {
				ModelID string `json:"modelId"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			models, err := s.service.ListManagedModels(r.Context(), subject)
			if err != nil {
				return nil, err
			}
			for _, model := range models {
				if model.ID == input.ModelID {
					return model, nil
				}
			}
			return nil, domain.NewError(404, "MODEL_NOT_FOUND", "模型目录记录不存在")
		default:
			return nil, domain.NewError(404, "TOOL_NOT_FOUND", "模型工具尚未开放")
		}
	}
	if purpose != control.MCPTokenPurposeApp || !strings.HasPrefix(name, "app_publish.") {
		return nil, domain.NewError(403, "MCP_TOOL_FORBIDDEN", "此令牌不能调用该工具")
	}
	ctx := r.Context()
	var appRef struct {
		AppID string `json:"app_id"`
	}
	switch name {
	case "app_publish.list":
		return s.service.PublicationDirectory(ctx, subject)
	case "app_publish.organizations":
		return s.service.PublicationOrganizations(ctx, subject)
	case "app_publish.get":
		if err := json.Unmarshal(arguments, &appRef); err != nil {
			return nil, err
		}
		return s.service.ManagedApp(ctx, subject, appRef.AppID)
	case "app_publish.readiness":
		if err := json.Unmarshal(arguments, &appRef); err != nil {
			return nil, err
		}
		return s.service.AppPublicationReadiness(ctx, subject, appRef.AppID)
	case "app_publish.publish":
		var input control.PublicationInput
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		return s.service.PublishRegisteredOCI(ctx, subject, input)
	case "app_publish.update":
		var input control.PublicationUpdateInput
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		return s.service.UpdatePublishedApp(ctx, subject, input)
	case "app_publish.disable", "app_publish.enable":
		var input control.PublicationStateInput
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		if name == "app_publish.disable" {
			return s.service.DisablePublishedApp(ctx, subject, input)
		}
		return s.service.EnablePublishedApp(ctx, subject, input)
	case "app_publish.grant_organizations", "app_publish.revoke_organizations":
		var input control.PublicationGrantInput
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		if name == "app_publish.grant_organizations" {
			return s.service.GrantPublishedApp(ctx, subject, input)
		}
		return s.service.RevokePublishedApp(ctx, subject, input)
	default:
		return nil, domain.NewError(404, "TOOL_NOT_FOUND", "应用发布工具不存在")
	}
}

func hubMCPTools(purpose string) []hubMCPTool {
	stringField := map[string]any{"type": "string"}
	integerField := map[string]any{"type": "integer"}
	stringsField := map[string]any{"type": "array", "items": stringField}
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	if purpose == control.MCPTokenPurposeModel {
		return []hubMCPTool{
			{Name: "model_publish.list", Description: "只读列出 Control 中已登记的模型资料；不代表网关已配置或可调用。", InputSchema: object(map[string]any{})},
			{Name: "model_publish.get", Description: "只读获取单个模型的 Control 目录资料；不包含供应商密钥。", InputSchema: object(map[string]any{"modelId": stringField}, "modelId")},
			{Name: "model_publish.gateway_models", Description: "只读列出 Control 专用 Token 当前在网关可见的模型 ID；不能代替渠道健康或计费检查。", InputSchema: object(map[string]any{})},
			{Name: "model_publish.gateway_snapshot", Description: "只读回读 new-api 中单个模型的脱敏渠道、计费和修订；未配置独立模型运营服务身份时不可用。", InputSchema: object(map[string]any{"modelId": stringField}, "modelId")},
			{Name: "model_publish.billing.preview", Description: "只读验证单模型计费规则，不保存配置，也不证明真实调用已扣费。mode 为 tiered_expr 或 per_request；分别提供 billingExpr 或 priceUsd（美元）。", InputSchema: object(map[string]any{"modelId": stringField, "mode": stringField, "billingExpr": stringField, "priceUsd": map[string]any{"type": "number"}}, "modelId", "mode")},
			{Name: "model_publish.validate", Description: "只读预检目录、默认组渠道、元数据、计费、专用 Token 可见性与已结算的探针收据；只有全部通过时 ready=true。", InputSchema: object(map[string]any{"modelId": stringField}, "modelId")},
			{Name: "model_publish.credentials.prepare", Description: "为内部 DeepSeek 渠道创建一次性凭据上传句柄；绝不可将上游密钥写进 MCP 参数，受信客户端须从本地密钥源直接 PUT 到返回的同域 uploadPath。", InputSchema: object(map[string]any{"modelId": stringField, "upstreamModelId": stringField, "channelName": stringField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "upstreamModelId", "channelName", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.credentials.rotate", Description: "为已存在的内部专用 DeepSeek 渠道申请一次性轮换句柄；密钥只能由受信客户端经 HTTPS 直传，不能放入 MCP 参数。", InputSchema: object(map[string]any{"modelId": stringField, "channelId": integerField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "channelId", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.credentials.status", Description: "只读查看一次性凭据句柄处理状态，不返回密钥。", InputSchema: object(map[string]any{"handle": stringField}, "handle")},
			{Name: "model_publish.group.preview", Description: "只读预览是否已配置专用 center-internal 内部测试分组及其修订；不修改客户分组。", InputSchema: object(map[string]any{})},
			{Name: "model_publish.group.apply", Description: "仅创建倍率为 1 的 center-internal 专用测试分组；不会修改已存在分组。须提供预览修订和幂等请求 ID。", InputSchema: object(map[string]any{"group": stringField, "requestId": stringField, "expectedGroupRevision": stringField}, "group", "requestId", "expectedGroupRevision")},
			{Name: "model_publish.gateway_model.configure", Description: "按模型 ID 修订 new-api 的描述、Chat Completions 端点与启停状态；须提供网关修订及幂等请求 ID。不涉及公开目录。", InputSchema: object(map[string]any{"modelId": stringField, "description": stringField, "endpoints": stringField, "status": integerField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "description", "endpoints", "status", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.billing.apply", Description: "按预览结果将单模型计费规则保存到 new-api；须提供网关修订及幂等请求 ID，不等于真实扣费探针通过。", InputSchema: object(map[string]any{"modelId": stringField, "mode": stringField, "billingExpr": stringField, "priceUsd": map[string]any{"type": "number"}, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "mode", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.channel.attach_alias", Description: "仅在 center-internal 专用渠道上给现有上游模型新增内部别名；不接受凭据或修改共享 default 渠道。", InputSchema: object(map[string]any{"modelId": stringField, "sourceModelId": stringField, "sourceChannelId": integerField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "sourceModelId", "sourceChannelId", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.channel.disable", Description: "仅停用只服务指定模型的 center-internal 专用渠道；共享渠道与客户分组一律拒绝。", InputSchema: object(map[string]any{"modelId": stringField, "channelId": integerField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "channelId", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.channel.update", Description: "仅更新未公开的 center-internal 专用 DeepSeek 渠道名称、上游模型映射、权重和优先级；拒绝共享及已预备公开路由。", InputSchema: object(map[string]any{"modelId": stringField, "channelId": integerField, "name": stringField, "upstreamModelId": stringField, "weight": integerField, "priority": integerField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "channelId", "name", "upstreamModelId", "weight", "priority", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.probe", Description: "执行一次真实、限额 0.01 美元的内部 DeepSeek 文本调用，并从 new-api 结算日志核对扣费；请求 ID 不得重复，网络失败先查状态，切勿盲重试。", InputSchema: object(map[string]any{"modelId": stringField, "requestId": stringField, "expectedGatewayRevision": stringField, "maxUsd": map[string]any{"type": "number", "const": 0.01}}, "modelId", "requestId", "expectedGatewayRevision", "maxUsd")},
			{Name: "model_publish.probe.status", Description: "按请求 ID 只读回查 new-api 的探针结算并恢复 Control 收据；不再次调用上游。", InputSchema: object(map[string]any{"modelId": stringField, "requestId": stringField, "expectedGatewayRevision": stringField}, "modelId", "requestId", "expectedGatewayRevision")},
			{Name: "model_publish.draft.save", Description: "仅保存不可公开、不开启体验的 Control 模型目录草稿；existing 记录须提供 expectedRevision。", InputSchema: object(map[string]any{"model": object(map[string]any{"id": stringField, "name": stringField, "provider": stringField, "summary": stringField, "categories": stringsField, "context": stringField, "maxInput": stringField, "maxOutput": stringField, "inputPrice": stringField, "outputPrice": stringField, "cachePrice": stringField, "priceUnit": stringField, "providerIconUrl": stringField}, "id", "name", "provider", "summary", "categories"), "expectedRevision": integerField}, "model", "expectedRevision")},
			{Name: "model_publish.unpublish", Description: "按 Control 目录修订关闭公开与在线体验；不删除或停用 new-api 渠道。", InputSchema: object(map[string]any{"modelId": stringField, "expectedRevision": integerField}, "modelId", "expectedRevision")},
			{Name: "model_publish.publish", Description: "用已结算的内部探针将模型分阶段公开：预备禁用的 default 路由、CAS 更新目录、启用路由并双端回读；结果不明时先查阶段，勿换请求 ID 重试。", InputSchema: object(map[string]any{"modelId": stringField, "sourceChannelId": integerField, "expectedCatalogRevision": integerField, "expectedGatewayRevision": stringField, "requestId": stringField}, "modelId", "sourceChannelId", "expectedCatalogRevision", "expectedGatewayRevision", "requestId")},
			{Name: "model_publish.publish.status", Description: "只读查询模型分阶段公开状态，用于不确定结果恢复；不会再次调用网关。", InputSchema: object(map[string]any{"modelId": stringField}, "modelId")},
			{Name: "model_publish.publish.resume", Description: "按原请求 ID 恢复未完成的分阶段上架；网关写入使用相同幂等键，绝不换请求 ID 盲重试。", InputSchema: object(map[string]any{"modelId": stringField, "requestId": stringField}, "modelId", "requestId")},
		}
	}
	if purpose != control.MCPTokenPurposeApp {
		return []hubMCPTool{}
	}
	showcaseProperties := map[string]any{
		"screenshots": stringsField, "highlights": stringsField, "whatsNew": stringField,
		"permissions": stringsField, "websiteUrl": stringField, "docsUrl": stringField,
		"sourceUrl": stringField, "license": stringField, "languages": stringField, "platforms": stringField,
	}
	chart := object(map[string]any{"ociRef": stringField, "version": stringField, "manifestDigest": stringField, "packageDigest": stringField},
		"ociRef", "version", "manifestDigest", "packageDigest")
	version := object(map[string]any{
		"version": stringField, "chart": chart, "releaseNotes": stringField, "sourceRevision": stringField,
		"upstreamVersion": stringField, "publisher": stringField, "sourceUrl": stringField,
		"licenseId": stringField, "licenseUrl": stringField,
	}, "version", "chart", "sourceRevision")
	editorial := map[string]any{
		"name": stringField, "category": stringField, "summary": stringField, "description": stringField, "developer": stringField,
		"brand": stringField, "iconRef": stringField, "publicIconUrl": stringField, "sortOrder": integerField,
	}
	publication := map[string]any{"appId": stringField, "groupId": stringField, "showcase": object(showcaseProperties, "screenshots", "highlights"),
		"version": version, "organizationIds": stringsField, "expectedRevision": integerField}
	update := map[string]any{"appId": stringField, "expectedRevision": integerField, "showcase": object(showcaseProperties)}
	for key, value := range editorial {
		publication[key] = value
		update[key] = value
	}
	appID := object(map[string]any{"app_id": stringField}, "app_id")
	state := object(map[string]any{"appId": stringField, "expectedRevision": integerField, "reason": stringField}, "appId", "expectedRevision")
	grants := object(map[string]any{"appId": stringField, "organizationIds": stringsField}, "appId", "organizationIds")
	return []hubMCPTool{
		{Name: "app_publish.list", Description: "列出 Hub 应用资料及当前 Station 目录回读，查看发布状态。", InputSchema: object(map[string]any{})},
		{Name: "app_publish.organizations", Description: "列出可授予安装权益的组织及其 ID。", InputSchema: object(map[string]any{})},
		{Name: "app_publish.get", Description: "读取单个应用的资料、修订、版本与启停状态；更新、停用前先读取修订。", InputSchema: appID},
		{Name: "app_publish.readiness", Description: "查询已有应用的上架必填字段缺口。", InputSchema: appID},
		{Name: "app_publish.publish", Description: "以完整资料快照、已发行固定 OCI Chart 双摘要和组织权益一次发布到 Station 目录；新建传 expectedRevision=0；开发者为 VerdantFlare 的自研应用可省略 licenseId/licenseUrl，第三方应用必填。不会安装应用。", InputSchema: object(publication, "appId", "name", "groupId", "category", "summary", "description", "developer", "showcase", "version", "organizationIds")},
		{Name: "app_publish.update", Description: "局部更新已有应用资料，只修改传入字段，传空字符串清空可选字段；不改版本、启停或权益。", InputSchema: object(update, "appId", "expectedRevision")},
		{Name: "app_publish.disable", Description: "停用分发：应用退出 Station 目录，已安装实例不受影响；须提供原因。", InputSchema: state},
		{Name: "app_publish.enable", Description: "恢复分发：复核资料与发布版本后重新进入 Station 目录。", InputSchema: state},
		{Name: "app_publish.grant_organizations", Description: "为已有应用授予组织安装权益。", InputSchema: grants},
		{Name: "app_publish.revoke_organizations", Description: "撤回组织安装权益；不卸载已安装实例。", InputSchema: grants},
	}
}
