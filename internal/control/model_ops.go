package control

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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

func (s *Service) GatewayGroupPreview(ctx context.Context, subject string) (gateway.ModelOpsGroupPreview, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsGroupPreview{}, err
	}
	client, ok := s.modelOpsGateway.(interface {
		GroupPreview(context.Context) (gateway.ModelOpsGroupPreview, error)
	})
	if !ok {
		return gateway.ModelOpsGroupPreview{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关分组配置接口尚未接入")
	}
	result, err := client.GroupPreview(ctx)
	if err != nil {
		return gateway.ModelOpsGroupPreview{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayGroupApply(ctx context.Context, subject string, input gateway.ModelOpsGroupApplyInput) (gateway.ModelOpsGroupResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsGroupResult{}, err
	}
	if input.Group != "center-internal" || !modelOpsRequestPattern.MatchString(input.RequestID) || !modelOpsRevisionPattern.MatchString(input.ExpectedGroupRevision) {
		return gateway.ModelOpsGroupResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "分组请求无效")
	}
	client, ok := s.modelOpsGateway.(interface {
		GroupApply(context.Context, gateway.ModelOpsGroupApplyInput) (gateway.ModelOpsGroupResult, error)
	})
	if !ok {
		return gateway.ModelOpsGroupResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关分组配置接口尚未接入")
	}
	result, err := client.GroupApply(ctx, input)
	if err != nil {
		return gateway.ModelOpsGroupResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayConfigureMetadata(ctx context.Context, subject string, input gateway.ModelOpsMetadataInput) (gateway.ModelOpsMetadataResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsMetadataResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsMetadataResult{}, err
	}
	if input.Endpoints != "/v1/chat/completions" || len(input.Description) < 10 || len(input.Description) > 2000 || strings.ContainsRune(input.Description, 0) || (input.Status != 0 && input.Status != 1) {
		return gateway.ModelOpsMetadataResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "模型元数据无效")
	}
	client, ok := s.modelOpsGateway.(interface {
		ConfigureMetadata(context.Context, gateway.ModelOpsMetadataInput) (gateway.ModelOpsMetadataResult, error)
	})
	if !ok {
		return gateway.ModelOpsMetadataResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关模型元数据接口尚未接入")
	}
	result, err := client.ConfigureMetadata(ctx, input)
	if err != nil {
		return gateway.ModelOpsMetadataResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayDisableChannel(ctx context.Context, subject string, input gateway.ModelOpsChannelDisableInput) (gateway.ModelOpsWriteResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if input.ChannelID <= 0 {
		return gateway.ModelOpsWriteResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "渠道 ID 无效")
	}
	client, ok := s.modelOpsGateway.(interface {
		DisableChannel(context.Context, gateway.ModelOpsChannelDisableInput) (gateway.ModelOpsWriteResult, error)
	})
	if !ok {
		return gateway.ModelOpsWriteResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关渠道停用接口尚未接入")
	}
	result, err := client.DisableChannel(ctx, input)
	if err != nil {
		return gateway.ModelOpsWriteResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayUpdateChannel(ctx context.Context, subject string, input gateway.ModelOpsChannelUpdateInput) (gateway.ModelOpsWriteResult, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return gateway.ModelOpsWriteResult{}, err
	}
	if input.ChannelID <= 0 || !modelOpsIDPattern.MatchString(input.UpstreamModelID) || strings.Contains(input.UpstreamModelID, "..") ||
		len(input.Name) < 3 || len(input.Name) > 80 || strings.ContainsAny(input.Name, "\x00\r\n") ||
		input.Weight < 1 || input.Weight > 100 || input.Priority < 0 || input.Priority > 100 {
		return gateway.ModelOpsWriteResult{}, domain.NewError(400, "MODEL_CONFIG_INVALID", "内部渠道资料无效")
	}
	client, ok := s.modelOpsGateway.(interface {
		UpdateChannel(context.Context, gateway.ModelOpsChannelUpdateInput) (gateway.ModelOpsWriteResult, error)
	})
	if !ok {
		return gateway.ModelOpsWriteResult{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关渠道更新接口尚未接入")
	}
	result, err := client.UpdateChannel(ctx, input)
	if err != nil {
		return gateway.ModelOpsWriteResult{}, modelOpsGatewayError(err)
	}
	return result, nil
}

func (s *Service) GatewayModelProbe(ctx context.Context, subject string, input gateway.ModelOpsProbeInput) (domain.ModelProbeReceipt, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	if err := validateModelOpsWrite(input.ModelID, input.RequestID, input.ExpectedGatewayRevision); err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	if input.MaxUSD != 0.01 {
		return domain.ModelProbeReceipt{}, domain.NewError(400, "MODEL_PROBE_BUDGET_INVALID", "探针单次预算必须为 0.01 美元")
	}
	client, ok := s.modelOpsGateway.(interface {
		Probe(context.Context, gateway.ModelOpsProbeInput) (gateway.ModelOpsProbeReceipt, error)
	})
	if !ok {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关受控探针接口尚未接入")
	}
	if prior, err := s.repository.ModelProbeReceipt(ctx, input.ModelID); err == nil && prior.GatewayRevision == input.ExpectedGatewayRevision {
		if prior.RequestID != input.RequestID {
			return domain.ModelProbeReceipt{}, domain.NewError(409, "MODEL_PROBE_ALREADY_VERIFIED", "当前网关修订已由另一请求完成扣费验证")
		}
		return prior, nil
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, input.ModelID)
	if err != nil {
		return domain.ModelProbeReceipt{}, modelOpsGatewayError(err)
	}
	if snapshot.Revision != input.ExpectedGatewayRevision {
		return domain.ModelProbeReceipt{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "网关模型配置已变化")
	}
	result, err := client.Probe(ctx, input)
	if err != nil {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_PROBE_OUTCOME_UNKNOWN", "探针结果未确认；请先按请求 ID 查询账单，勿重复付费调用")
	}
	if !result.Verified || result.ModelID != input.ModelID || result.RequestID != input.RequestID || result.BilledUSD <= 0 || result.BilledUSD > 0.01 {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_PROBE_CHARGE_INVALID", "探针实际扣费未通过核验")
	}
	receipt := domain.ModelProbeReceipt{ModelID: input.ModelID, RequestID: input.RequestID,
		GatewayRevision: input.ExpectedGatewayRevision, BilledQuota: result.BilledQuota,
		BilledUSD: result.BilledUSD, VerifiedAt: time.Now().UTC()}
	if err := s.repository.SaveModelProbeReceipt(ctx, receipt); err != nil {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_PROBE_RECEIPT_UNAVAILABLE", "探针扣费已发生但记录失败；请按请求 ID 恢复")
	}
	return receipt, nil
}

func (s *Service) GatewayModelProbeStatus(ctx context.Context, subject, modelID, requestID, revision string) (domain.ModelProbeReceipt, error) {
	if err := s.requireOperationsRole(ctx, subject, "api_ops_admin"); err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	if err := validateModelOpsWrite(modelID, requestID, revision); err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	client, ok := s.modelOpsGateway.(interface {
		ProbeCharge(context.Context, string) (gateway.ModelOpsProbeCharge, error)
	})
	if !ok {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_CONFIG_UNAVAILABLE", "网关探针账单接口尚未接入")
	}
	charge, err := client.ProbeCharge(ctx, requestID)
	if err != nil || charge.ModelID != modelID || !charge.Verified || charge.BilledUSD <= 0 || charge.BilledUSD > 0.01 {
		return domain.ModelProbeReceipt{}, domain.NewError(409, "MODEL_PROBE_NOT_SETTLED", "探针账单尚未确认；勿重复提交")
	}
	snapshot, err := s.modelOpsGateway.Snapshot(ctx, modelID)
	if err != nil || snapshot.Revision != revision {
		return domain.ModelProbeReceipt{}, domain.NewError(409, "MODEL_CONFIG_CHANGED", "探针后网关配置已变化")
	}
	receipt := domain.ModelProbeReceipt{ModelID: modelID, RequestID: requestID, GatewayRevision: revision,
		BilledQuota: charge.BilledQuota, BilledUSD: charge.BilledUSD, VerifiedAt: time.Now().UTC()}
	if err := s.repository.SaveModelProbeReceipt(ctx, receipt); err != nil {
		return domain.ModelProbeReceipt{}, domain.NewError(503, "MODEL_PROBE_RECEIPT_UNAVAILABLE", "探针记录保存失败")
	}
	return receipt, nil
}
