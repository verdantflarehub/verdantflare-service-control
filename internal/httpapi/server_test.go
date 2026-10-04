package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestContextRequiresAuthentication(t *testing.T) {
	server := newTestServer(t, config.Config{})
	response := request(t, server, http.MethodGet, "/api/control/context", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestContextMatchesHubContract(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodGet, "/api/control/context", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var context domain.CenterContext
	decode(t, response, &context)
	if context.CenterUserID != "cu_01HUB7C9Q" || context.ActiveOrganizationID != "org_verdantflare" {
		t.Fatalf("unexpected context: %+v", context)
	}
	if len(context.Organizations) != 2 || len(context.Organizations[0].Roles) == 0 {
		t.Fatalf("organization memberships missing: %+v", context.Organizations)
	}
}

func TestOverviewAggregatesCurrentOrganization(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodGet, "/api/control/overview", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var overview domain.Overview
	decode(t, response, &overview)
	if overview.AvailableApps != 6 || overview.Organization.OrganizationID != "org_verdantflare" {
		t.Fatalf("unexpected overview: %+v", overview)
	}
}

func TestExperienceSessionUnavailableWithoutRuntime(t *testing.T) {
	server := newTestServer(t, testConfig())
	createdResponse := request(t, server, http.MethodPost, "/api/control/experience/sessions", map[string]any{"appId": "wan-video", "region": "cn-east-1"})
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, body = %s", createdResponse.StatusCode, readBody(t, createdResponse))
	}
}

func TestAPIKeyUnavailableUntilGatewayIntegration(t *testing.T) {
	server := newTestServer(t, testConfig())
	createdResponse := request(t, server, http.MethodPost, "/api/control/api-keys", map[string]any{"name": "CI 测试", "scopes": []string{"models:read", "tasks:write"}})
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, body = %s", createdResponse.StatusCode, readBody(t, createdResponse))
	}
}

func TestOperationsRoleRecheckedAfterOrganizationSwitch(t *testing.T) {
	server := newTestServer(t, testConfig())
	switchResponse := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	defer switchResponse.Body.Close()
	if switchResponse.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d, body = %s", switchResponse.StatusCode, readBody(t, switchResponse))
	}

	response := request(t, server, http.MethodGet, "/api/control/ops/releases", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestUnknownJSONFieldRejected(t *testing.T) {
	server := newTestServer(t, testConfig())
	response := request(t, server, http.MethodPost, "/api/control/experience/sessions", map[string]any{"appId": "wan-video", "region": "cn-east-1", "admin": true})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestManagedAppPublicationAndEntitlement(t *testing.T) {
	server := newTestServer(t, testConfig())
	appID := "workflow-demo"
	created := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{
		"id": appID, "name": "Workflow Demo", "version": "0.1.0", "category": "视觉创作", "summary": "测试应用",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create app status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()

	market := request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var before []domain.App
	decode(t, market, &before)
	market.Body.Close()
	if containsApp(before, appID) {
		t.Fatal("candidate app appeared in customer Market")
	}

	updated := request(t, server, http.MethodPatch, "/api/control/ops/apps/"+appID, map[string]any{
		"name": "Workflow Demo", "version": "0.1.0", "category": "视觉创作", "summary": "测试应用", "channel": "Preview",
	})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	updated.Body.Close()

	market = request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var withoutGrant []domain.App
	decode(t, market, &withoutGrant)
	market.Body.Close()
	if containsApp(withoutGrant, appID) {
		t.Fatal("unentitled app appeared in customer Market")
	}
	public := request(t, server, http.MethodPatch, "/api/control/ops/apps/"+appID, map[string]any{
		"name": "Workflow Demo", "version": "0.1.0", "category": "视觉创作", "summary": "测试应用", "channel": "Preview", "publicVisible": true,
	})
	if public.StatusCode != http.StatusOK {
		t.Fatalf("public publish status = %d, body = %s", public.StatusCode, readBody(t, public))
	}
	public.Body.Close()
	market = request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var publicApps []domain.App
	decode(t, market, &publicApps)
	market.Body.Close()
	if !containsApp(publicApps, appID) {
		t.Fatal("public app missing from customer Market")
	}
	for _, app := range publicApps {
		if app.ID == appID && app.Entitled {
			t.Fatal("public app was incorrectly marked entitled")
		}
	}
	publicDetail := request(t, server, http.MethodGet, "/api/control/market/apps/"+appID, nil)
	if publicDetail.StatusCode != http.StatusOK {
		t.Fatalf("public app detail status = %d", publicDetail.StatusCode)
	}
	var publicApp domain.App
	decode(t, publicDetail, &publicApp)
	publicDetail.Body.Close()
	if publicApp.Entitled {
		t.Fatal("public app detail was incorrectly marked entitled")
	}

	organization := request(t, server, http.MethodGet, "/api/control/ops/organizations/org_verdantflare", nil)
	var managed domain.ManagedOrganization
	decode(t, organization, &managed)
	organization.Body.Close()
	if organization.StatusCode != http.StatusOK {
		t.Fatalf("get organization status = %d", organization.StatusCode)
	}
	appIDs := append(managed.AppIDs, appID)
	grant := request(t, server, http.MethodPatch, "/api/control/ops/organizations/org_verdantflare", map[string]any{
		"plan": "Enterprise", "status": "正常", "appIds": appIDs,
		"expectedEntitlementVersion": managed.Organization.EntitlementVersion,
	})
	if grant.StatusCode != http.StatusOK {
		t.Fatalf("grant status = %d, body = %s", grant.StatusCode, readBody(t, grant))
	}
	var granted domain.ManagedOrganization
	decode(t, grant, &granted)
	grant.Body.Close()
	if granted.Organization.EntitlementVersion != managed.Organization.EntitlementVersion+1 {
		t.Fatalf("entitlement version = %d", granted.Organization.EntitlementVersion)
	}

	market = request(t, server, http.MethodGet, "/api/control/market/apps", nil)
	var entitled []domain.App
	decode(t, market, &entitled)
	market.Body.Close()
	if !containsApp(entitled, appID) {
		t.Fatal("published entitled app missing from customer Market")
	}
	for _, app := range entitled {
		if app.ID == appID && !app.Entitled {
			t.Fatal("granted app was not marked entitled")
		}
	}

	stale := request(t, server, http.MethodPatch, "/api/control/ops/organizations/org_verdantflare", map[string]any{
		"plan": "Enterprise", "status": "正常", "appIds": appIDs,
		"expectedEntitlementVersion": managed.Organization.EntitlementVersion,
	})
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale version status = %d, body = %s", stale.StatusCode, readBody(t, stale))
	}
	stale.Body.Close()

	frozen := request(t, server, http.MethodPatch, "/api/control/ops/organizations/org_verdantflare", map[string]any{
		"plan": "Enterprise", "status": "冻结", "appIds": appIDs,
		"expectedEntitlementVersion": granted.Organization.EntitlementVersion,
	})
	if frozen.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("freeze without gateway must fail closed: status = %d, body = %s", frozen.StatusCode, readBody(t, frozen))
	}
	frozen.Body.Close()
}

func TestManagedWriteRequiresCurrentOperationsRole(t *testing.T) {
	server := newTestServer(t, testConfig())
	switched := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	if switched.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d, body = %s", switched.StatusCode, readBody(t, switched))
	}
	switched.Body.Close()
	response := request(t, server, http.MethodPost, "/api/control/ops/apps", map[string]any{"id": "forbidden-demo", "name": "Forbidden Demo", "version": "0.1.0"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("create app status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func TestCreateOrganizationAndMemberRecord(t *testing.T) {
	server := newTestServer(t, testConfig())
	created := request(t, server, http.MethodPost, "/api/control/ops/organizations", map[string]any{"name": "测试客户组织", "plan": "Pilot"})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create organization status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var organization domain.ManagedOrganization
	decode(t, created, &organization)
	created.Body.Close()
	if organization.Organization.EntitlementVersion != 1 || len(organization.AppIDs) != 0 {
		t.Fatalf("unexpected new organization: %+v", organization)
	}
	read := request(t, server, http.MethodGet, "/api/control/ops/organizations/"+organization.Organization.OrganizationID, nil)
	if read.StatusCode != http.StatusOK {
		t.Fatalf("read organization status = %d, body = %s", read.StatusCode, readBody(t, read))
	}
	read.Body.Close()
	modified := request(t, server, http.MethodPatch, "/api/control/ops/organizations/"+organization.Organization.OrganizationID, map[string]any{
		"name": "客户组织新名称", "shortName": "客户", "defaultRegion": "cn-north-1", "industry": "影视制作", "billingEmail": "billing@example.test",
		"plan": "Studio", "status": "正常", "appIds": []string{}, "expectedEntitlementVersion": organization.Organization.EntitlementVersion,
	})
	if modified.StatusCode != http.StatusOK {
		t.Fatalf("update organization status = %d, body = %s", modified.StatusCode, readBody(t, modified))
	}
	var modifiedOrganization domain.ManagedOrganization
	decode(t, modified, &modifiedOrganization)
	modified.Body.Close()
	if modifiedOrganization.Organization.Name != "客户组织新名称" || modifiedOrganization.Organization.DefaultRegion != "cn-north-1" || modifiedOrganization.Organization.BillingEmail != "billing@example.test" {
		t.Fatalf("organization fields not updated: %+v", modifiedOrganization.Organization)
	}

	invited := request(t, server, http.MethodPost, "/api/control/settings/members", map[string]any{"email": "new.member@example.test", "role": "成员"})
	if invited.StatusCode != http.StatusCreated {
		t.Fatalf("create member record status = %d, body = %s", invited.StatusCode, readBody(t, invited))
	}
	invited.Body.Close()
	list := request(t, server, http.MethodGet, "/api/control/settings/members", nil)
	var members []domain.Member
	decode(t, list, &members)
	list.Body.Close()
	found := false
	for _, member := range members {
		if member.Email == "new.member@example.test" && member.Status == "待邀请" {
			found = true
		}
	}
	if !found {
		t.Fatal("saved pending member record missing from list")
	}
}

func TestManagedOrganizationMemberWritesAndRoleGuard(t *testing.T) {
	server := newTestServer(t, testConfig())
	path := "/api/control/ops/organizations/org_northshore/members"
	created := request(t, server, http.MethodPost, path, map[string]string{"email": "member@example.test", "role": "成员"})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create member status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var member domain.Member
	decode(t, created, &member)
	created.Body.Close()
	if member.ID == "" || member.Status != "待邀请" {
		t.Fatalf("unexpected pending member: %+v", member)
	}
	updated := request(t, server, http.MethodPatch, path+"/"+member.ID, map[string]string{"role": "财务查看者", "status": "已取消"})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update member status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	updated.Body.Close()
	read := request(t, server, http.MethodGet, "/api/control/ops/organizations/org_northshore", nil)
	var organization domain.ManagedOrganization
	decode(t, read, &organization)
	read.Body.Close()
	found := false
	for _, item := range organization.Members {
		found = found || item.ID == member.ID && item.Role == "财务查看者" && item.Status == "已取消"
	}
	if !found {
		t.Fatal("updated member missing from managed organization")
	}
	switched := request(t, server, http.MethodPut, "/api/control/context/active-organization", map[string]string{"organizationId": "org_northshore"})
	switched.Body.Close()
	denied := request(t, server, http.MethodPatch, path+"/"+member.ID, map[string]string{"role": "成员", "status": "待邀请"})
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("role check status = %d, body = %s", denied.StatusCode, readBody(t, denied))
	}
	denied.Body.Close()
}

func TestHubPageDataEndpointsAndAPIKeyLifecycle(t *testing.T) {
	server := newTestServer(t, testConfig())
	for _, path := range []string{
		"/api/control/overview", "/api/control/api/models", "/api/control/market/apps",
		"/api/control/settings/organization", "/api/control/settings/members",
		"/api/control/ops/releases", "/api/control/ops/organizations",
	} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	for _, path := range []string{"/api/control/settings/billing", "/api/control/api/usage", "/api/control/api-keys"} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET %s status = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	revoked := request(t, server, http.MethodDelete, "/api/control/api-keys/key_prod_31", nil)
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke key status = %d, body = %s", revoked.StatusCode, readBody(t, revoked))
	}
	revoked.Body.Close()
}

func containsApp(apps []domain.App, id string) bool {
	for _, app := range apps {
		if app.ID == id {
			return true
		}
	}
	return false
}

func newTestServer(t *testing.T, configuration config.Config) *httptest.Server {
	t.Helper()
	repository := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
	service := control.NewService(repository)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(configuration, service, logger))
	t.Cleanup(server.Close)
	return server
}

func testConfig() config.Config {
	return config.Config{Environment: "test", DevLoginSubject: "logto_01vf9k2"}
}

func request(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
