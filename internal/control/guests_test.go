package control

import (
	"context"
	"errors"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/gateway"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

type fakeDirectory struct {
	users []gateway.LoginDirectoryUser
	fail  bool
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
