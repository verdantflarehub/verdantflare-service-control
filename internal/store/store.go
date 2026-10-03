package store

import (
	"context"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type Repository interface {
	CenterContext(context.Context, string) (domain.CenterContext, error)
	SetActiveOrganization(context.Context, string, string) (domain.CenterContext, error)
	ActiveOrganization(context.Context, string) (domain.Organization, []string, string, error)
	Organization(context.Context, string) (domain.Organization, error)
	UpdateOrganization(context.Context, string, domain.Organization) (domain.Organization, error)

	ListApps(context.Context, string) ([]domain.App, error)
	GetApp(context.Context, string, string) (domain.App, error)

	ListExperienceSessions(context.Context, string) ([]domain.ExperienceSession, error)
	CreateExperienceSession(context.Context, string, string, domain.App, string, time.Duration, int) (domain.ExperienceSession, error)
	CloseExperienceSession(context.Context, string, string) (domain.ExperienceSession, error)

	ListAPIKeys(context.Context, string) ([]domain.APIKey, error)
	CreateAPIKey(context.Context, string, domain.APIKey) (domain.APIKey, error)
	RevokeAPIKey(context.Context, string, string) error
	ListModels(context.Context, string) ([]domain.Model, error)
	ListAPITasks(context.Context, string) ([]domain.APITask, error)
	Usage(context.Context, string) (domain.UsageSummary, error)

	ListMembers(context.Context, string) ([]domain.Member, error)
	AddMember(context.Context, string, domain.Member) (domain.Member, error)
	UpdateMember(context.Context, string, string, string, string, string) (domain.Member, error)
	Billing(context.Context, string) (domain.BillingSummary, error)

	ListReleases(context.Context) ([]domain.Release, error)
	ListOperationsOrganizations(context.Context) ([]domain.OperationsOrganization, error)
	CreateManagedApp(context.Context, domain.App) (domain.ManagedApp, error)
	ManagedApp(context.Context, string) (domain.ManagedApp, error)
	UpdateManagedApp(context.Context, string, domain.App) (domain.ManagedApp, error)
	PublicCatalog(context.Context) (domain.PublicCatalog, error)
	ListManagedModels(context.Context) ([]domain.PublicModel, error)
	CreateManagedModel(context.Context, domain.PublicModel) (domain.PublicModel, error)
	UpdateManagedModel(context.Context, string, domain.PublicModel) (domain.PublicModel, error)
	CreateManagedOrganization(context.Context, domain.Organization) (domain.ManagedOrganization, error)
	ManagedOrganization(context.Context, string) (domain.ManagedOrganization, error)
	UpdateManagedOrganization(context.Context, string, domain.Organization, []string, int) (domain.ManagedOrganization, error)
}
