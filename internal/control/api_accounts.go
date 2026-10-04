package control

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type GrantAPICreditInput struct {
	RequestID   string `json:"requestId"`
	AmountCents int    `json:"amountCents"`
}

func gatewayAccountError(err error) error {
	if err == nil {
		return nil
	}
	var gatewayError gateway.CenterHTTPError
	if errors.As(err, &gatewayError) {
		switch gatewayError.Status {
		case 400:
			return domain.NewError(400, "gateway_request_invalid", "网关拒绝了无效请求")
		case 403:
			return domain.NewError(403, "gateway_forbidden", "网关账号已停用")
		case 404:
			return domain.NewError(404, "gateway_key_not_found", "网关 Key 不存在")
		case 409:
			return domain.NewError(409, "gateway_request_conflict", "请求重复但内容不同，或额度超过上限")
		}
	}
	return domain.NewError(503, "gateway_accounts_unavailable", "模型网关账号服务暂不可用")
}

func (s *Service) ProbeAPIKey(ctx context.Context, subject, keyID string) (gateway.CenterProbe, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return gateway.CenterProbe{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return gateway.CenterProbe{}, domain.NewError(403, "api_key_forbidden", "当前角色不能测试 API Key")
	}
	if s.accountGateway == nil {
		return gateway.CenterProbe{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	if !strings.HasPrefix(keyID, "gw_") {
		return gateway.CenterProbe{}, domain.NewError(400, "legacy_key_unavailable", "历史 Control Key 不能测试网关连通性")
	}
	id, err := strconv.Atoi(strings.TrimPrefix(keyID, "gw_"))
	if err != nil || id < 1 {
		return gateway.CenterProbe{}, domain.NewError(400, "api_key_invalid", "无效的 Key ID")
	}
	result, err := s.accountGateway.ProbeKey(ctx, organization.OrganizationID, id)
	return result, gatewayAccountError(err)
}

func (s *Service) OrganizationAPICredit(ctx context.Context, subject, organizationID string) (gateway.CenterBalance, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return gateway.CenterBalance{}, err
	}
	if _, err := s.repository.ManagedOrganization(ctx, organizationID); err != nil {
		return gateway.CenterBalance{}, err
	}
	if s.accountGateway == nil {
		return gateway.CenterBalance{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	result, err := s.accountGateway.Balance(ctx, organizationID)
	return result, gatewayAccountError(err)
}

func (s *Service) GrantOrganizationAPICredit(ctx context.Context, subject, organizationID string, input GrantAPICreditInput) (gateway.CenterBalance, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return gateway.CenterBalance{}, err
	}
	organization, err := s.repository.ManagedOrganization(ctx, organizationID)
	if err != nil {
		return gateway.CenterBalance{}, err
	}
	if organization.Organization.Status == "冻结" {
		return gateway.CenterBalance{}, domain.NewError(403, "organization_frozen", "冻结组织不能增加 API 额度")
	}
	if !gatewayRequestIDPattern.MatchString(input.RequestID) || input.AmountCents < 1 || input.AmountCents > 100000 {
		return gateway.CenterBalance{}, domain.NewError(400, "credit_grant_invalid", "请输入 0.01–1000.00 美元并提供唯一请求 ID")
	}
	if s.accountGateway == nil {
		return gateway.CenterBalance{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	result, err := s.accountGateway.Grant(ctx, organizationID, input.RequestID, subject, input.AmountCents)
	return result, gatewayAccountError(err)
}
