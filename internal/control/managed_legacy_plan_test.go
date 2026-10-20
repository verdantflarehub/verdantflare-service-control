package control

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestUpdateManagedOrganizationPreservesLegacyEmptyPlan(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemorySeeded(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	const orgID = "org_verdantflare"
	const subject = "00000000-0000-4000-8000-000000000001"
	current, err := repo.ManagedOrganization(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.UpdateManagedOrganization(ctx, orgID, domain.Organization{Plan: "", Status: "正常"}, current.AppIDs, current.Organization.EntitlementVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateManagedApp(ctx, domain.App{ID: "app-publishing", Name: "应用发布", GroupID: "system", Channel: "Stable", Visibility: "listed", Enabled: true, PublishedVersion: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	appIDs := append(append([]string(nil), legacy.AppIDs...), "app-publishing")
	updated, err := NewService(repo).UpdateManagedOrganization(ctx, subject, orgID, UpdateManagedOrganizationInput{
		Plan: "", Status: "正常", AppIDs: appIDs, ExpectedEntitlementVersion: legacy.Organization.EntitlementVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Organization.Plan != "" || !slices.Contains(updated.AppIDs, "app-publishing") {
		t.Fatalf("legacy plan or app grant lost: plan=%q appIDs=%v", updated.Organization.Plan, updated.AppIDs)
	}
}

func TestUpdateManagedOrganizationCannotClearExistingPlan(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemorySeeded(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	current, err := repo.ManagedOrganization(ctx, "org_verdantflare")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewService(repo).UpdateManagedOrganization(ctx, "00000000-0000-4000-8000-000000000001", "org_verdantflare", UpdateManagedOrganizationInput{
		Plan: "", Status: "正常", AppIDs: current.AppIDs, ExpectedEntitlementVersion: current.Organization.EntitlementVersion,
	})
	if err == nil {
		t.Fatal("clearing an existing plan must be rejected")
	}
}
