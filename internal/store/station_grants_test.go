package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestStationGrantPersistsAndRevokesWithoutChangingVersion(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryBootstrap()
	const appID, version = "internal-app", "1.0.0"
	if _, err := memory.CreateManagedApp(ctx, domain.App{ID: appID, Name: "Internal App", GroupID: "image"}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.CreateAppVersion(ctx, domain.AppVersion{AppID: appID, Version: version, Manifest: json.RawMessage(`{"app_id":"internal-app"}`)}); err != nil {
		t.Fatal(err)
	}
	grant := domain.StationTestGrant{
		StationID: "0199f879-1234-7abc-8abc-0123456789ab", OrganizationID: "org_verdantflare",
		AppID: appID, Version: version, CertificateSHA256: "test-fingerprint",
		ExpiresAt: time.Now().Add(time.Hour).UTC(), CreatedAt: time.Now().UTC(),
	}
	if _, err := memory.CreateStationTestGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	raw, err := encodeMemory(memory)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeMemory(raw)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := restored.StationTestGrantsForStation(ctx, grant.StationID)
	if err != nil || len(listed) != 1 || listed[0].Revision != 1 {
		t.Fatalf("grant was not persisted: %+v, %v", listed, err)
	}
	revoked, err := restored.RevokeStationTestGrant(ctx, appID, version, grant.StationID)
	if err != nil || !revoked.Revoked || revoked.Revision != 2 {
		t.Fatalf("grant was not revoked: %+v, %v", revoked, err)
	}
	grant.CreatedAt = grant.CreatedAt.Add(time.Second)
	renewed, err := restored.CreateStationTestGrant(ctx, grant)
	if err != nil || renewed.Revision != 3 {
		t.Fatalf("same certificate could not be renewed after revocation: %+v, %v", renewed, err)
	}
	sealed, err := restored.AppVersion(ctx, appID, version)
	if err != nil || string(sealed.Manifest) != `{"app_id":"internal-app"}` {
		t.Fatalf("version changed when revoking grant: %+v, %v", sealed, err)
	}
}
