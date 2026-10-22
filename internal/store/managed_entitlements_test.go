package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestDisabledPublishedAppIsNotAnEntitlementOption(t *testing.T) {
	ctx := context.Background()
	m := NewMemorySeeded(time.Now().UTC())
	const appID = "app-publishing"
	const organizationID = "org_verdantflare"
	m.apps[appID] = domain.App{ID: appID, Name: "应用发布", Channel: "Stable", Enabled: false, DisabledReason: "已由 Hub MCP 替代", PublishedVersion: "0.1.0"}
	m.entitlements[organizationID][appID] = true // Existing grant must not be silently erased.

	organization, err := m.ManagedOrganization(ctx, organizationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range organization.Apps {
		if app.ID == appID {
			t.Fatal("disabled app remained a grant option")
		}
	}
	if slices.Contains(organization.AppIDs, appID) {
		t.Fatal("disabled app remained in visible grants")
	}
	if _, err := m.UpdateManagedOrganization(ctx, organizationID, domain.Organization{Plan: organization.Organization.Plan, Status: "正常"}, append(organization.AppIDs, appID), organization.Organization.EntitlementVersion); err == nil {
		t.Fatal("organization editor accepted a disabled app")
	}
	if _, err := m.UpdateManagedOrganization(ctx, organizationID, domain.Organization{Plan: organization.Organization.Plan, Status: "正常"}, organization.AppIDs, organization.Organization.EntitlementVersion); err != nil {
		t.Fatal(err)
	}
	if !m.entitlements[organizationID][appID] {
		t.Fatal("editing other entitlements silently revoked historical grant")
	}
}

func TestRetiredPublishedAppLeavesActiveReleaseListButKeepsHistory(t *testing.T) {
	ctx := context.Background()
	m := NewMemorySeeded(time.Now().UTC())
	const appID = "app-publishing"
	m.apps[appID] = domain.App{ID: appID, Name: "应用发布", Channel: "Stable", Visibility: "hidden", Enabled: false, DisabledReason: "已由 Hub MCP 替代", PublishedVersion: "0.1.0"}

	releases, err := m.ListReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range releases {
		if release.AppID == appID {
			t.Fatal("retired app remains in active releases")
		}
	}
	managed, err := m.ManagedApp(ctx, appID)
	if err != nil || managed.App.PublishedVersion != "0.1.0" {
		t.Fatalf("retired version cannot be audited: %+v, %v", managed, err)
	}
	if managed.Release.Status != "已停用" {
		t.Fatalf("retired app shows misleading release status: %s", managed.Release.Status)
	}

	app := m.apps[appID]
	app.Visibility = "listed"
	m.apps[appID] = app
	releases, err = m.ListReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(releases, func(release domain.Release) bool { return release.AppID == appID }) {
		t.Fatal("ordinary disabled app was hidden from operations")
	}
}
