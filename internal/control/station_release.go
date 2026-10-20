package control

import (
	"strings"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// stationReleaseVisible is the single distribution gate shared by the
// Station catalog, version readback and installation grant. A published
// pointer does not override an operator's explicit disable action.
func stationReleaseVisible(app domain.App) bool {
	listed := app.Visibility == "listed" || app.Channel == "Listed" || app.Channel == "Preview" || app.Channel == "Stable"
	return listed && app.Enabled && strings.TrimSpace(app.DisabledReason) == ""
}

func stationCurrentVersion(app domain.App) string {
	if version := strings.TrimSpace(app.PublishedVersion); version != "" {
		return version
	}
	if !app.InstallGrantRequired && (app.Channel == "Stable" || app.Channel == "Preview") {
		return strings.TrimSpace(app.Version)
	}
	return ""
}
