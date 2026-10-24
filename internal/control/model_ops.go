package control

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

var modelOpsIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
var modelOpsRequestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)
var modelOpsRevisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func modelOpsGatewayError(err error) error {
	var upstream gateway.CenterHTTPError
	if errors.As(err, &upstream) {
		switch upstream.Status {
		case http.StatusBadRequest:
			return domain.NewError(400, "MODEL_CONFIG_INVALID", "网关拒绝了模型配置参数")
		case http.StatusConflict:
			return domain.NewError(409, "MODEL_CONFIG_CHANGED", "网关模型配置已变化，请重新回读后重试")
		}
	}
	return domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置写入或回读不可用")
}

func validateModelOpsWrite(modelID, requestID, revision string) error {
	if !modelOpsIDPattern.MatchString(modelID) || strings.Contains(modelID, "..") ||
		!modelOpsRequestPattern.MatchString(requestID) || !modelOpsRevisionPattern.MatchString(revision) {
		return domain.NewError(400, "MODEL_CONFIG_INVALID", "模型 ID、请求 ID 或修订无效")
	}
	return nil
}

func (s *Service) GatewayModelSnapshot(ctx context.Context, subject, modelID string) (gateway.ModelOpsSnapshot, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsSnapshot{}, err
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || len(modelID) > 128 || strings.ContainsAny(modelID, "\x00\r\n") {
		return gateway.ModelOpsSnapshot{}, domain.NewError(400, "model_id_invalid", "模型 ID 无效")
	}
	if s.modelOpsGateway == nil {
		return gateway.ModelOpsSnapshot{}, domain.NewError(503, "model_gateway_ops_unavailable", "网关模型配置接口尚未接入")
	}
	return s.modelOpsGateway.Snapshot(ctx, modelID)
}

// GatewayBillingPreview is validation only; no model becomes available or
// public as a result. Write orchestration is kept separate from Hub MCP until
// the credential and publication gates are complete.
func (s *Service) GatewayBillingPreview(ctx context.Context, subject string, input gateway.ModelOpsBillingInput) (gateway.ModelOpsBillingPreview, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsBillingPreview{}, err
	}
	if !modelOpsIDPattern.MatchString(input.ModelID) || strings.Contains(input.ModelID, "..") {
		return gateway.ModelOpsBillingPreview{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "模型 ID 无效")
	}
	if s.modelOpsGateway == nil {
		return gateway.ModelOpsBillingPreview{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	result, err := s.modelOpsGateway.BillingPreview(ctx, input)
	if err != nil {
		return gateway.ModelOpsBillingPreview{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayBillingApply(ctx context.Context, subject string, input gateway.ModelOpsBillingInput) (gateway.ModelOpsWriteResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if s.modelOpsGateway == nil {
		return gateway.ModelOpsWriteResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	result, err := s.modelOpsGateway.BillingApply(ctx, input)
	if err != nil {
		return gateway.ModelOpsWriteResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayAttachAlias(ctx context.Context, subject string, input gateway.ModelOpsAliasInput) (gateway.ModelOpsWriteResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if !modelOpsIDPattern.MatchString(input.SourceModelID) || strings.Contains(input.SourceModelID, "..") || input.SourceChannelID <= 0 || input.SourceModelID == input.ModelID {
		return gateway.ModelOpsWriteResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "来源模型或渠道无效")
	}
	if s.modelOpsGateway == nil {
		return gateway.ModelOpsWriteResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型配置接口尚未接入")
	}
	result, err := s.modelOpsGateway.AttachAlias(ctx, input)
	if err != nil {
		return gateway.ModelOpsWriteResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}
