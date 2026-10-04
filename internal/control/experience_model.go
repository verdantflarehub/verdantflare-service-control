package control

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
)

const firstExperienceModel = "deepseek-flash"

type CreateModelExperienceInput struct {
	RequestID string `json:"requestId"`
	ModelID   string `json:"modelId"`
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
	if !gatewayRequestIDPattern.MatchString(input.RequestID) || input.ModelID != firstExperienceModel || input.Prompt == "" || utf8.RuneCountInString(input.Prompt) > 2000 {
		return domain.ModelExperienceRun{}, domain.NewError(400, "experience_request_invalid", "模型、请求 ID 或提示词无效")
	}
	existing, found, err := s.repository.FindModelExperienceRunByRequestID(ctx, organization.OrganizationID, input.RequestID)
	if err != nil {
		return domain.ModelExperienceRun{}, err
	}
	if found {
		if existing.CenterUserID == userID && existing.ModelID == input.ModelID && existing.Prompt == input.Prompt {
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
		if item.ID == firstExperienceModel {
			available = true
			break
		}
	}
	if !available {
		return domain.ModelExperienceRun{}, domain.NewError(503, "experience_model_unavailable", "该模型当前未上架或网关不可用")
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
		CenterUserID: userID, ModelID: input.ModelID, Prompt: input.Prompt,
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
	result, err := s.accountGateway.ExperienceChat(ctx, run.OrganizationID, run.Prompt)
	status, response, errorCode := "completed", result.Response, ""
	if err != nil {
		status, response, errorCode = "failed", "", "upstream_rejected"
		var gatewayError gateway.CenterHTTPError
		if errors.As(err, &gatewayError) {
			if gatewayError.Status == 402 {
				errorCode = "insufficient_quota"
			} else if gatewayError.Status == 403 {
				errorCode = "organization_disabled"
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
		status, response, errorCode, result.PromptTokens, result.OutputTokens, result.TotalTokens); finishErr != nil {
		slog.Error("persist model experience outcome failed", "run_id", run.ID, "error", finishErr)
	}
}

func (s *Service) SweepModelExperienceRuns(ctx context.Context) error {
	return s.repository.SweepModelExperienceRuns(ctx, s.now())
}
