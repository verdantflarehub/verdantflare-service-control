package control

import (
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"testing"
)

func TestReleaseRequiresInstallGrantForPublisherBootstrap(t *testing.T) {
	for _, tc := range []struct {
		appID     string
		requested bool
		want      bool
	}{
		{appID: "app-publishing", requested: false, want: true},
		{appID: "app-publishing", requested: true, want: true},
		{appID: "image-mcp", requested: false, want: false},
		{appID: "image-mcp", requested: true, want: true},
	} {
		service := &Service{}
		service.SetRequiredInstallGrantApps([]string{"app-publishing"})
		if got := service.releaseRequiresInstallGrant(domain.App{ID: tc.appID}, tc.requested); got != tc.want {
			t.Fatalf("releaseRequiresInstallGrant(%q, %v) = %v, want %v", tc.appID, tc.requested, got, tc.want)
		}
	}
	service := &Service{}
	if !service.releaseRequiresInstallGrant(domain.App{ID: "custom-admin", InstallGrantRequired: true}, false) {
		t.Fatal("existing grant policy must survive a version change")
	}
}
