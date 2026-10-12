package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type fakeDirectory struct {
	users []gateway.LoginDirectoryUser
	fail  bool
}

type pagedDirectory struct {
	pages map[string]gateway.LoginDirectoryPage
}

func (d pagedDirectory) ListUsers(_ context.Context, cursor, _ string, _ int) (gateway.LoginDirectoryPage, error) {
	return d.pages[cursor], nil
}
func (d pagedDirectory) User(context.Context, string) (gateway.LoginDirectoryUser, error) {
	return gateway.LoginDirectoryUser{}, errors.New("unused")
}

func TestGuestListSkipsBoundPagesBeforeReportingEmpty(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemoryBootstrap()
	directory := pagedDirectory{pages: map[string]gateway.LoginDirectoryPage{
		"":     {Users: []gateway.LoginDirectoryUser{{ID: "00000000-0000-4000-8000-000000000001", Email: "admin@example.test", Status: "active", EmailVerified: true}}, NextCursor: "next"},
		"next": {Users: []gateway.LoginDirectoryUser{{ID: "waiting-user", Email: "waiting@example.test", Status: "active", EmailVerified: true}}},
	}}
	service := NewService(repository)
	service.SetLoginDirectory(directory)
	page, err := service.ListGuests(ctx, "00000000-0000-4000-8000-000000000001", "", "", 10)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != "waiting-user" || page.NextCursor != "" {
		t.Fatalf("filtered first Login page must not appear empty: %+v, %v", page, err)
	}
}

func TestOrganizationAdminSelectsUnboundUserAndTransfersOnlyBetweenManagedOrganizations(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemorySeeded(time.Now().UTC())
	directory := &fakeDirectory{users: []gateway.LoginDirectoryUser{{ID: "new-user", Email: "new@example.test", Status: "active", EmailVerified: true}}}
	service := NewService(repository)
	service.SetLoginDirectory(directory)
	const admin = "00000000-0000-4000-8000-000000000001"
	page, err := service.ListCurrentOrganizationGuests(ctx, admin, "", "", 50)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != "new-user" {
		t.Fatalf("unbound selection = %+v, %v", page, err)
	}
	member, err := service.BindCurrentOrganizationMember(ctx, admin, BindMemberInput{LoginUserID: "new-user", Role: "成员"})
	if err != nil || member.ID != "new-user" {
		t.Fatalf("bind current organization = %+v, %v", member, err)
	}
	page, err = service.ListCurrentOrganizationGuests(ctx, admin, "", "", 50)
	if err != nil || len(page.Users) != 0 {
		t.Fatalf("bound user remained selectable = %+v, %v", page, err)
	}
	moved, err := service.TransferCurrentOrganizationMember(ctx, admin, member.ID, TransferMemberInput{TargetOrganizationID: "org_northshore", Role: "开发者"})
	if err != nil || moved.Role != "开发者" {
		t.Fatalf("transfer = %+v, %v", moved, err)
	}
	context, err := repository.CenterContext(ctx, member.ID)
	if err != nil || context.ActiveOrganizationID != "org_northshore" || len(context.Organizations) != 1 {
		t.Fatalf("transferred context = %+v, %v", context, err)
	}

	sourceOnly, err := repository.BindLoginUser(ctx, "org_verdantflare", "source-admin", "source-admin@example.test", "组织管理员")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repository.BindLoginUser(ctx, "org_verdantflare", "other-user", "other@example.test", "成员")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransferCurrentOrganizationMember(ctx, sourceOnly.ID, other.ID, TransferMemberInput{TargetOrganizationID: "org_northshore", Role: "成员"}); err == nil {
		t.Fatal("source-only administrator transferred a member into an unmanaged target")
	}
}

func (d *fakeDirectory) ListUsers(_ context.Context, _, _ string, _ int) (gateway.LoginDirectoryPage, error) {
	if d.fail {
		return gateway.LoginDirectoryPage{}, errors.New("offline")
	}
	return gateway.LoginDirectoryPage{Users: d.users}, nil
}
func (d *fakeDirectory) User(_ context.Context, id string) (gateway.LoginDirectoryUser, error) {
	if d.fail {
		return gateway.LoginDirectoryUser{}, errors.New("offline")
	}
	for _, user := range d.users {
		if user.ID == id {
			return user, nil
		}
	}
	return gateway.LoginDirectoryUser{}, errors.New("not found")
}

func TestGuestsAndBindingRequireOperationsRole(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemoryBootstrap()
	_, err := repository.CreateManagedOrganization(ctx, domain.Organization{OrganizationID: "org_DV97FRRNE4Q", Name: "演示用户"})
	if err != nil {
		t.Fatal(err)
	}
	directory := &fakeDirectory{users: []gateway.LoginDirectoryUser{
		{ID: "guest-id", Email: "guest@example.test", Status: "active", EmailVerified: true},
		{ID: "disabled-id", Email: "disabled@example.test", Status: "disabled", EmailVerified: true},
		{ID: "unverified-id", Email: "unverified@example.test", Status: "active"},
	}}
	service := NewService(repository)
	service.SetLoginDirectory(directory)
	const admin = "00000000-0000-4000-8000-000000000001"
	page, err := service.ListGuests(ctx, admin, "", "", 50)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != "guest-id" {
		t.Fatalf("guests: %+v %v", page, err)
	}
	if _, err := service.BindManagedMember(ctx, admin, "org_DV97FRRNE4Q", BindMemberInput{LoginUserID: "disabled-id", Role: "成员"}); err == nil {
		t.Fatal("disabled user was bound")
	}
	member, err := service.BindManagedMember(ctx, admin, "org_DV97FRRNE4Q", BindMemberInput{LoginUserID: "guest-id", Role: "组织管理员"})
	if err != nil || member.CenterUserID == "" {
		t.Fatalf("bind: %+v %v", member, err)
	}
	if _, err := service.ListGuests(ctx, "guest-id", "", "", 50); err == nil {
		t.Fatal("organization admin must not read operations directory")
	}
	if _, err := service.BindManagedMember(ctx, "guest-id", "org_DV97FRRNE4Q", BindMemberInput{LoginUserID: "unverified-id", Role: "成员"}); err == nil {
		t.Fatal("organization admin must not bind")
	}
	page, err = service.ListGuests(ctx, admin, "", "", 50)
	if err != nil || len(page.Users) != 0 {
		t.Fatalf("bound user remains guest: %+v %v", page, err)
	}
	directory.fail = true
	if _, err := service.ListGuests(ctx, admin, "", "", 50); err == nil {
		t.Fatal("directory failure must fail closed")
	}
}
