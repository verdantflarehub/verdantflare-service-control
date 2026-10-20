package control

import (
	"reflect"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestPublicationReadinessRequiresCustomerFacingMetadata(t *testing.T) {
	app := domain.App{ID: "test-app", Revision: 3, Name: "A", Category: "创作", Summary: "摘要"}
	got := publicationReadiness(app)
	want := []string{"name", "developer", "description", "showcase.screenshots", "showcase.highlights"}
	if got.Ready || got.Revision != 3 || !reflect.DeepEqual(got.MissingFields, want) {
		t.Fatalf("unexpected readiness: %+v", got)
	}
	app.Name = "Test App"
	app.Developer = "VerdantFlare"
	app.Description = "完整说明"
	app.Showcase = domain.AppShowcase{Screenshots: []string{"https://example.test/screen.png"}, Highlights: []string{"生成内容"}}
	got = publicationReadiness(app)
	if !got.Ready || len(got.MissingFields) != 0 {
		t.Fatalf("complete metadata should be ready: %+v", got)
	}
}
