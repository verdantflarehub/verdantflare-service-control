package control

import (
	"context"
	"slices"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type StationInstallGrant struct {
	AppID    string `json:"appId"`
	Version  string `json:"version"`
	Required bool   `json:"required"`
	Allowed  bool   `json:"allowed"`
}

// StationInstallGrant checks the current release pointer and, for releases
// published through Studio, the current Control organization entitlement.
func (s *Service) StationInstallGrant(ctx context.Context, appID, version, controlOrganizationID string) (StationInstallGrant, error) {
	managed, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return StationInstallGrant{}, err
	}
	app := managed.App
	legacyRelease := !app.InstallGrantRequired && app.PublishedVersion == "" && app.Version == version && (app.Channel == "Stable" || app.Channel == "Preview")
	if (!legacyRelease && app.PublishedVersion != version) || (!app.Enabled && !legacyRelease) || app.Visibility != "listed" {
		return StationInstallGrant{}, domain.NewError(403, "RELEASE_NOT_INSTALLABLE", "当前版本未开放安装")
	}
	result := StationInstallGrant{AppID: appID, Version: version, Required: app.InstallGrantRequired, Allowed: !app.InstallGrantRequired}
	if !app.InstallGrantRequired {
		return result, nil
	}
	if controlOrganizationID == "" {
		return result, nil
	}
	org, err := s.repository.ManagedOrganization(ctx, controlOrganizationID)
	if err != nil {
		return result, nil
	}
	result.Allowed = org.Organization.Status == "正常" && slices.Contains(org.AppIDs, appID)
	return result, nil
}
