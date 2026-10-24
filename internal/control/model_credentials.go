package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type PrepareModelCredentialInput struct {
	ModelID                 string `json:"modelId"`
	UpstreamModelID         string `json:"upstreamModelId"`
	ChannelName             string `json:"channelName"`
	ChannelID               int    `json:"channelId,omitempty"`
	RequestID               string `json:"requestId"`
	ExpectedGatewayRevision string `json:"expectedGatewayRevision"`
}

type ModelCredentialStatus struct {
	ModelID   string    `json:"modelId"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
	ChannelID int       `json:"channelId,omitempty"`
}

type ModelCredentialPreparation struct {
	Handle     string    `json:"handle"`
	UploadPath string    `json:"uploadPath"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func modelCredentialDigest(handle string) string {
	digest := sha256.Sum256([]byte(handle))
	return hex.EncodeToString(digest[:])
}

func (s *Service) PrepareModelCredential(ctx context.Context, identity MCPIdentity, input PrepareModelCredentialInput) (ModelCredentialPreparation, error) {
	return s.prepareModelCredential(ctx, identity, input, "create")
}

func (s *Service) PrepareModelCredentialRotation(ctx context.Context, identity MCPIdentity, input PrepareModelCredentialInput) (ModelCredentialPreparation, error) {
	return s.prepareModelCredential(ctx, identity, input, "rotate")
}

func (s *Service) prepareModelCredential(ctx context.Context, identity MCPIdentity, input PrepareModelCredentialInput, action string) (ModelCredentialPreparation, error) {
	if identity.Purpose != MCPTokenPurposeModel || identity.TokenID == "" {
		return ModelCredentialPreparation{}, domain.NewError(403, "MCP_TOOL_FORBIDDEN", "此令牌不能配置模型凭据")
	}
	if err := s.requireOperationsRole(ctx, identity.Subject, "api_ops_admin"); err != nil {
		return ModelCredentialPreparation{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return ModelCredentialPreparation{}, err
	}
	if action == "create" {
		if input.ChannelID != 0 || !modelOpsIDPattern.MatchString(input.UpstreamModelID) || strings.Contains(input.UpstreamModelID, "..") ||
			len(input.ChannelName) < 3 || len(input.ChannelName) > 80 || strings.ContainsAny(input.ChannelName, "\x00\r\n") {
			return ModelCredentialPreparation{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "渠道资料无效")
		}
	} else if action == "rotate" {
		if input.ChannelID <= 0 || input.ChannelName != "" || input.UpstreamModelID != "" {
			return ModelCredentialPreparation{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "轮换目标无效")
		}
	} else {
		return ModelCredentialPreparation{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "凭据操作无效")
	}
	if s.modelOpsGateway == nil {
		return ModelCredentialPreparation{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, input.ModelID)
	if err != nil {
		return ModelCredentialPreparation{}, modelOpsGatewayError(err)
	}
	if snapshot.Revision != input.ExpectedGatewayRevision {
		return ModelCredentialPreparation{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "网关模型配置已变化")
	}
	if action == "rotate" {
		matched := false
		for _, channel := range snapshot.Channels {
			if channel.ID == input.ChannelID && channel.Type == 43 && len(channel.Groups) == 1 && channel.Groups[0] == "center-internal" {
				matched = true
			}
		}
		if !matched {
			return ModelCredentialPreparation{}, domain.NewError(409, "MODEL_CHANNEL_NOT_DEDICATED", "轮换目标不是专用内部渠道")
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ModelCredentialPreparation{}, domain.NewError(503, "MODEL_CREDENTIAL_UNAVAILABLE", "无法生成凭据句柄")
	}
	handle := "vfcred_" + base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	ticket := domain.ModelCredentialTicket{HandleHash: modelCredentialDigest(handle), Action: action, TokenID: identity.TokenID,
		LoginSubject: identity.Subject, ModelID: input.ModelID, UpstreamModelID: input.UpstreamModelID,
		ChannelName: input.ChannelName, TargetChannelID: input.ChannelID, RequestID: input.RequestID,
		ExpectedGatewayRevision: input.ExpectedGatewayRevision, ExpiresAt: expiresAt, Status: "prepared"}
	if err := s.repository.CreateModelCredentialTicket(ctx, ticket); err != nil {
		return ModelCredentialPreparation{}, err
	}
	return ModelCredentialPreparation{Handle: handle, UploadPath: "/api/control/mcp/credentials/" + handle, ExpiresAt: expiresAt}, nil
}

func (s *Service) ModelCredentialStatus(ctx context.Context, identity MCPIdentity, handle string) (ModelCredentialStatus, error) {
	if identity.Purpose != MCPTokenPurposeModel || !strings.HasPrefix(handle, "vfcred_") || len(handle) != 50 {
		return ModelCredentialStatus{}, domain.NewError(404, "MODEL_CREDENTIAL_HANDLE_NOT_FOUND", "凭据句柄不存在")
	}
	ticket, err := s.repository.ModelCredentialTicket(ctx, modelCredentialDigest(handle), identity.TokenID, identity.Subject)
	if err != nil {
		return ModelCredentialStatus{}, err
	}
	return ModelCredentialStatus{ModelID: ticket.ModelID, Status: ticket.Status, ExpiresAt: ticket.ExpiresAt, ChannelID: ticket.ChannelID}, nil
}

func (s *Service) UploadModelCredential(ctx context.Context, identity MCPIdentity, handle, credential string) (ModelCredentialStatus, error) {
	if identity.Purpose != MCPTokenPurposeModel || !strings.HasPrefix(handle, "vfcred_") || len(handle) != 50 {
		return ModelCredentialStatus{}, domain.NewError(404, "MODEL_CREDENTIAL_HANDLE_NOT_FOUND", "凭据句柄不存在")
	}
	if err := s.requireOperationsRole(ctx, identity.Subject, "api_ops_admin"); err != nil {
		return ModelCredentialStatus{}, err
	}
	if len(credential) < 16 || len(credential) > 2048 || strings.ContainsAny(credential, "\x00\r\n\t ") {
		return ModelCredentialStatus{}, domain.NewError(400, "MODEL_CREDENTIAL_INVALID", "上游凭据格式无效")
	}
	writer, canCreate := s.modelOpsGateway.(interface {
		CreateChannel(context.Context, gateway.ModelOpsChannelCreateInput) (gateway.ModelOpsChannelCreateResult, error)
	})
	rotator, canRotate := s.modelOpsGateway.(interface {
		RotateCredential(context.Context, gateway.ModelOpsCredentialRotateInput) (gateway.ModelOpsWriteResult, error)
	})
	if !canCreate && !canRotate {
		return ModelCredentialStatus{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关渠道写入接口尚未接入")
	}
	digest := modelCredentialDigest(handle)
	ticket, err := s.repository.ConsumeModelCredentialTicket(ctx, digest, identity.TokenID, identity.Subject, time.Now().UTC())
	if err != nil {
		return ModelCredentialStatus{}, err
	}
	channelID := ticket.TargetChannelID
	if ticket.Action == "create" && !canCreate || ticket.Action == "rotate" && !canRotate {
		_ = s.repository.FinishModelCredentialTicket(ctx, digest, "unknown", 0)
		return ModelCredentialStatus{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关凭据操作不可用")
	}
	var operationErr error
	if ticket.Action == "create" {
		result, err := writer.CreateChannel(ctx, gateway.ModelOpsChannelCreateInput{
			ModelID: ticket.ModelID, UpstreamModelID: ticket.UpstreamModelID, Name: ticket.ChannelName,
			Credential: credential, RequestID: ticket.RequestID, ExpectedGatewayRevision: ticket.ExpectedGatewayRevision,
		})
		channelID, operationErr = result.ChannelID, err
	} else {
		_, operationErr = rotator.RotateCredential(ctx, gateway.ModelOpsCredentialRotateInput{
			ModelID: ticket.ModelID, ChannelID: ticket.TargetChannelID, Credential: credential,
			RequestID: ticket.RequestID, ExpectedGatewayRevision: ticket.ExpectedGatewayRevision,
		})
	}
	status := "applied"
	if operationErr != nil {
		status = "unknown"
		var upstream gateway.CenterHTTPError
		if errors.As(operationErr, &upstream) && (upstream.Status == http.StatusBadRequest || upstream.Status == http.StatusConflict) {
			status = "conflict"
		}
	}
	if finishErr := s.repository.FinishModelCredentialTicket(ctx, digest, status, channelID); finishErr != nil {
		return ModelCredentialStatus{}, domain.NewError(503, "MODEL_CREDENTIAL_STATUS_UNKNOWN", "渠道请求结果待回读")
	}
	if operationErr != nil {
		return ModelCredentialStatus{}, modelOpsGatewayError(operationErr)
	}
	return ModelCredentialStatus{ModelID: ticket.ModelID, Status: status, ExpiresAt: ticket.ExpiresAt, ChannelID: channelID}, nil
}
