package control

import "testing"

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
		if got := releaseRequiresInstallGrant(tc.appID, tc.requested); got != tc.want {
			t.Fatalf("releaseRequiresInstallGrant(%q, %v) = %v, want %v", tc.appID, tc.requested, got, tc.want)
		}
	}
}
