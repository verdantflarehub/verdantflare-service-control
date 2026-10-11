package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type directoryStub struct{ user gateway.LoginDirectoryUser }

func (d directoryStub) ListUsers(context.Context, string, string, int) (gateway.LoginDirectoryPage, error) {
	return gateway.LoginDirectoryPage{Users: []gateway.LoginDirectoryUser{d.user}}, nil
}
func (d directoryStub) User(context.Context, string) (gateway.LoginDirectoryUser, error) {
	return d.user, nil
}

func TestGuestHTTPBindingUpdatesContextAndMemberList(t *testing.T) {
	repository := store.NewMemoryBootstrap()
	_, err := repository.CreateManagedOrganization(context.Background(), domain.Organization{OrganizationID: "org_DV97FRRNE4Q", Name: "演示用户"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.AddMember(context.Background(), "org_DV97FRRNE4Q", domain.Member{Email: "guest@example.test", Role: "组织管理员", Status: "待邀请"})
	if err != nil {
		t.Fatal(err)
	}
	service := control.NewService(repository)
	service.SetLoginDirectory(directoryStub{gateway.LoginDirectoryUser{ID: "guest-login-id", Email: "guest@example.test", Status: "active", EmailVerified: true}})
	server := httptest.NewServer(httpapi.New(config.Config{DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guests := request(t, server, http.MethodGet, "/api/control/ops/guests", nil)
	if guests.StatusCode != http.StatusOK {
		t.Fatalf("guests status %d: %s", guests.StatusCode, readBody(t, guests))
	}
	guests.Body.Close()
	bound := request(t, server, http.MethodPost, "/api/control/ops/organizations/org_DV97FRRNE4Q/members/bind", map[string]string{"loginUserId": "guest-login-id", "role": "组织管理员"})
	if bound.StatusCode != http.StatusOK {
		t.Fatalf("bind status %d: %s", bound.StatusCode, readBody(t, bound))
	}
	bound.Body.Close()
	center, err := repository.CenterContext(context.Background(), "guest-login-id")
	if err != nil || center.ActiveOrganizationID != "org_DV97FRRNE4Q" {
		t.Fatalf("context: %+v %v", center, err)
	}
	organization, err := repository.ManagedOrganization(context.Background(), "org_DV97FRRNE4Q")
	if err != nil || len(organization.Members) != 1 || organization.Members[0].CenterUserID == "" {
		t.Fatalf("members: %+v %v", organization.Members, err)
	}
}

func TestSettingsPickerAndOperationsTransferHTTP(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemoryBootstrap()
	if _, err := repository.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: "org_demo", Name: "演示组织"}); err != nil {
		t.Fatal(err)
	}
	service := control.NewService(repository)
	service.SetLoginDirectory(directoryStub{gateway.LoginDirectoryUser{ID: "new-login-user", Email: "new@example.test", Status: "active", EmailVerified: true}})
	server := httptest.NewServer(httpapi.New(config.Config{DevLoginSubject: "00000000-0000-4000-8000-000000000001"}, service, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	for _, check := range []struct {
		method string
		path   string
		body   map[string]string
	}{
		{http.MethodGet, "/api/control/settings/guests", nil},
		{http.MethodPost, "/api/control/settings/members/bind", map[string]string{"loginUserId": "new-login-user", "role": "成员"}},
		{http.MethodPost, "/api/control/ops/organizations/org_verdantflare/members/new-login-user/transfer", map[string]string{"targetOrganizationId": "org_demo", "role": "开发者"}},
	} {
		response := request(t, server, check.method, check.path, check.body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d %s", check.method, check.path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
	center, err := repository.CenterContext(ctx, "new-login-user")
	if err != nil || center.ActiveOrganizationID != "org_demo" || len(center.Organizations) != 1 || center.Organizations[0].Roles[0] != "developer" {
		t.Fatalf("context after transfer: %+v %v", center, err)
	}
}
