package control

import (
	"context"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

func (s *Service) GatewayModelSnapshot(ctx context.Context, subject, modelID string) (gateway.ModelOpsSnapshot, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsSnapshot{}, err
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || len(modelID) > 128 || strings.ContainsAny(modelID, "\x00\r\n") {
		return gateway.ModelOpsSnapshot{}, domain.NewError(400, "model_id_invalid", "模型 ID 无效")
	}
	if s.modelOpsGateway == nil {
		return gateway.ModelOpsSnapshot{}, domain.NewError(503, "model_gateway_ops_unavailable", "网关模型配置只读接口尚未接入")
	}
	return s.modelOpsGateway.Snapshot(ctx, modelID)
}
