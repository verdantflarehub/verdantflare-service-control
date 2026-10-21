package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestPublicationPreflightDoesNotCreateIncompleteApp(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		showcase domain.AppShowcase
		digest   string
	}{
		{name: "missing showcase", digest: "sha256:" + strings.Repeat("a", 64)},
		{name: "invalid digest", showcase: domain.AppShowcase{Screenshots: []string{"https://example.test/screenshot.png"}, Highlights: []string{"Creates images"}}, digest: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := store.NewMemorySeeded(time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC))
			service := NewService(repo)
			_, err := service.PublishRegisteredOCI(ctx, "00000000-0000-4000-8000-000000000001", PublicationInput{
				AppID: "preflight-app", Name: "Preflight App", GroupID: "desktop", Category: "Tool", Summary: "Summary", Description: "Description", Developer: "Test",
				Showcase: &tc.showcase, OrganizationIDs: []string{"org_verdantflare"},
				Version: CreateOCIAppVersionInput{Version: "1.0.0", Chart: &CreateOCIChartInput{OCIRef: "oci://registry.verdantflare.ai/charts/preflight-app", Version: "1.0.0", ManifestDigest: tc.digest, PackageDigest: tc.digest}, SourceRevision: "abcdef", LicenseID: "MIT", LicenseURL: "https://example.test/license"},
			})
			if err == nil {
				t.Fatal("expected preflight rejection")
			}
			if _, err := repo.ManagedApp(ctx, "preflight-app"); err == nil {
				t.Fatal("preflight failure left an orphan app")
			}
		})
	}
}
