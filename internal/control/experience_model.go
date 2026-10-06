package control

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

type CreateModelExperienceInput struct {
	RequestID string `json:"requestId"`
	ModelID   string `json:"modelId"`
	KeyID     string `json:"keyId"`
	Prompt    string `json:"prompt"`
}

func (s *Service) ListModelExperienceRuns(ctx context.Context, subject string) ([]domain.ModelExperienceRun, error) {
	organization, _, userID, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	return s.repository.ListModelExperienceRuns(ctx, organization.OrganizationID, userID)
}

func (s *Service) GetModelExperienceRun(ctx context.Context, subject, runID string) (domain.ModelExperienceRun, error) {
	organization, _, userID, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	return s.repository.GetModelExperienceRun(ctx, organization.OrganizationID, userID, runID)
}

func (s *Service) CreateModelExperienceRun(ctx context.Context, subject string, input CreateModelExperienceInput) (domain.ModelExperienceRun, error) {
	organization, _, userID, err := s.activeOrganization(ctx, subject)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	keyID, keyErr := strconv.Atoi(strings.TrimPrefix(input.KeyID, "gw_"))
	if !gatewayRequestIDPattern.MatchString(input.RequestID) || !appIDPattern.MatchString(input.ModelID) || input.Prompt == "" || utf8.RuneCountInString(input.Prompt) > 2000 {
		return domain.ModelExperienceRun{}, domain.NewError(400, "experience_request_invalid", "模型、请求 ID 或提示词无效")
	}
	if !strings.HasPrefix(input.KeyID, "gw_") || keyErr != nil || keyID < 1 {
		return domain.ModelExperienceRun{}, domain.NewError(400, "experience_key_required", "请先创建并选择当前组织的 API Key")
	}
	existing, found, err := s.repository.FindModelExperienceRunByRequestID(ctx, organization.OrganizationID, input.RequestID)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	if found {
		if existing.CenterUserID == userID && existing.ModelID == input.ModelID && existing.KeyID == input.KeyID && existing.Prompt == input.Prompt {
			return existing, nil
		}
		return domain.ModelExperienceRun{}, domain.NewError(409, "experience_request_conflict", "请求 ID 已用于其他体验任务")
	}
	if organization.Status == "冻结" {
		return domain.ModelExperienceRun{}, domain.NewError(403, "organization_frozen", "组织已冻结")
	}
	if s.accountGateway == nil || s.modelGateway == nil {
		return domain.ModelExperienceRun{}, domain.NewError(503, "experience_gateway_unconfigured", "模型体验网关尚未配置")
	}
	published, err := s.publishedGatewayModels(ctx)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	available := false
	for _, item := range published {
		if item.ID == input.ModelID && item.ExperienceMode == "chat" {
			available = true
			break
		}
	}
	if !available {
		return domain.ModelExperienceRun{}, domain.NewError(403, "experience_model_disabled", "该模型尚未开放 Hub 在线体验")
	}
	probe, err := s.accountGateway.ProbeKey(ctx, organization.OrganizationID, keyID)
	if err != nil {
		return domain.ModelExperienceRun{}, gatewayAccountError(err)
	}
	if !probe.OK || !slices.Contains(probe.Models, input.ModelID) {
		if probe.Reason == "insufficient_quota" {
			return domain.ModelExperienceRun{}, domain.NewError(402, "experience_credit_insufficient", "组织 API 额度不足")
		}
		return domain.ModelExperienceRun{}, domain.NewError(403, "experience_key_unavailable", "所选 API Key 已失效或未授权此模型")
	}
	balance, err := s.accountGateway.Balance(ctx, organization.OrganizationID)
	if err != nil {
		return domain.ModelExperienceRun{}, gatewayAccountError(err)
	}
	if !balance.Enabled || balance.RemainingQuota <= 0 {
		return domain.ModelExperienceRun{}, domain.NewError(402, "experience_credit_insufficient", "组织 API 额度不足或账号已停用")
	}
	select {
	case s.modelRunSlots <- struct{}{}:
	default:
		return domain.ModelExperienceRun{}, domain.NewError(503, "experience_busy", "模型体验繁忙，请稍后再试")
	}
	now := s.now()
	run := domain.ModelExperienceRun{
		ID: newID("mrun"), RequestID: input.RequestID, OrganizationID: organization.OrganizationID,
		CenterUserID: userID, ModelID: input.ModelID, KeyID: input.KeyID, Prompt: input.Prompt,
		Status: "submitting", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	stored, created, err := s.repository.CreateModelExperienceRun(ctx, run)
	if err != nil || !created {
		<-s.modelRunSlots
		return stored, err
	}
	go s.executeModelExperienceRun(stored)
	return stored, nil
}

func (s *Service) executeModelExperienceRun(run domain.ModelExperienceRun) {
	defer func() { <-s.modelRunSlots }()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	keyID, _ := strconv.Atoi(strings.TrimPrefix(run.KeyID, "gw_"))
	result, err := s.accountGateway.ExperienceChat(ctx, run.OrganizationID, keyID, run.ModelID, run.Prompt)
	status, response, errorCode := "completed", result.Response, ""
	if err != nil {
		status, response, errorCode = "failed", "", "upstream_rejected"
		var gatewayError gateway.CenterHTTPError
		if errors.As(err, &gatewayError) {
			if gatewayError.Status == 402 {
				errorCode = "insufficient_quota"
			} else if gatewayError.Status == 403 {
				errorCode = "key_unavailable"
			} else if gatewayError.Status == 404 {
				errorCode = "key_not_found"
			} else if gatewayError.Status == 408 || gatewayError.Status >= 500 {
				status, errorCode = "outcome_unknown", "upstream_result_unknown"
			}
		} else {
			status, errorCode = "outcome_unknown", "upstream_result_unknown"
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if _, finishErr := s.repository.FinishModelExperienceRun(finishCtx, run.OrganizationID, run.CenterUserID, run.ID,
		status, response, errorCode, result.PromptTokens, result.OutputTokens, result.TotalTokens, result.BilledQuota); finishErr != nil {
		slog.Error("persist model experience outcome failed", "run_id", run.ID, "error", finishErr)
	}
}

func (s *Service) SweepModelExperienceRuns(ctx context.Context) error {
	return s.repository.SweepModelExperienceRuns(ctx, s.now())
}
