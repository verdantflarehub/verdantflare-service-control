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
	RequestID string `json:"requestId"`
	AmountFen int    `json:"amountFen"`
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

func (s *Service) ProbeAPIKey(ctx context.Context, subject, keyID string) (APIKeyProbe, error) {
	organization, roles, _, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return APIKeyProbe{}, err
	}
	if !hasAnyRole(roles, "organization_admin") {
		return APIKeyProbe{}, domain.NewError(403, "api_key_forbidden", "当前角色不能测试 API Key")
	}
	if s.accountGateway == nil {
		return APIKeyProbe{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	if !strings.HasPrefix(keyID, "gw_") {
		return APIKeyProbe{}, domain.NewError(400, "legacy_key_unavailable", "历史 Control Key 不能测试网关连通性")
	}
	id, err := strconv.Atoi(strings.TrimPrefix(keyID, "gw_"))
	if err != nil || id < 1 {
		return APIKeyProbe{}, domain.NewError(400, "api_key_invalid", "无效的 Key ID")
	}
	result, err := s.accountGateway.ProbeKey(ctx, organization.OrganizationID, id)
	if err != nil {
		return APIKeyProbe{}, gatewayAccountError(err)
	}
	return APIKeyProbe{OK: result.OK, Reason: result.Reason, Models: result.Models, Currency: "CNY",
		ExchangeRateCnyPerUSD: cnyPerUSD, RemainingYuan: quotaYuan(int64(result.RemainingQuota)),
		RemainingQuota: result.RemainingQuota, ReadOnly: result.ReadOnly}, nil
}

func (s *Service) OrganizationAPICredit(ctx context.Context, subject, organizationID string) (APICreditBalance, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return APICreditBalance{}, err
	}
	if _, err := s.repository.ManagedOrganization(ctx, organizationID); err != nil {
		return APICreditBalance{}, err
	}
	if s.accountGateway == nil {
		return APICreditBalance{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	result, err := s.accountGateway.Balance(ctx, organizationID)
	if err != nil {
		return APICreditBalance{}, gatewayAccountError(err)
	}
	return creditBalance(result), nil
}

func (s *Service) QuoteOrganizationAPICredit(ctx context.Context, subject, organizationID string, amountFen int) (APICreditQuote, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return APICreditQuote{}, err
	}
	organization, err := s.repository.ManagedOrganization(ctx, organizationID)
	if err != nil {
		return APICreditQuote{}, err
	}
	if organization.Organization.Status == "冻结" {
		return APICreditQuote{}, domain.NewError(403, "organization_frozen", "冻结组织不能增加 API 额度")
	}
	cents, ok := gatewayCentsFromFen(amountFen)
	if !ok {
		return APICreditQuote{}, domain.NewError(400, "credit_grant_invalid", "请输入可折算为网关 0.01–1000.00 美元的人民币金额")
	}
	return creditQuote(amountFen, cents), nil
}

func (s *Service) GrantOrganizationAPICredit(ctx context.Context, subject, organizationID string, input GrantAPICreditInput) (APICreditBalance, error) {
	if err := s.requireOperationsRole(ctx, subject, "customer_success_admin"); err != nil {
		return APICreditBalance{}, err
	}
	organization, err := s.repository.ManagedOrganization(ctx, organizationID)
	if err != nil {
		return APICreditBalance{}, err
	}
	if organization.Organization.Status == "冻结" {
		return APICreditBalance{}, domain.NewError(403, "organization_frozen", "冻结组织不能增加 API 额度")
	}
	cents, ok := gatewayCentsFromFen(input.AmountFen)
	if !gatewayRequestIDPattern.MatchString(input.RequestID) || !ok {
		return APICreditBalance{}, domain.NewError(400, "credit_grant_invalid", "请输入可折算为网关 0.01–1000.00 美元的人民币金额，并提供唯一请求 ID")
	}
	if s.accountGateway == nil {
		return APICreditBalance{}, domain.NewError(503, "gateway_accounts_unconfigured", "模型网关账号服务尚未配置")
	}
	result, err := s.accountGateway.Grant(ctx, organizationID, input.RequestID, subject, cents)
	if err != nil {
		return APICreditBalance{}, gatewayAccountError(err)
	}
	return creditBalance(result), nil
}
