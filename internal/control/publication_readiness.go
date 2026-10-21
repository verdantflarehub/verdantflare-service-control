package control

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func validEditorialURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func validateEditorialFields(app domain.App) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(app.Summary)); n > 512 {
		return domain.NewError(400, "app_summary_invalid", "一句话简介最多 512 个字符")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(app.Description)); n > 16384 {
		return domain.NewError(400, "app_description_invalid", "应用介绍最多 16384 个字符")
	}
	if app.PublicIconURL != "" && !validEditorialURL(app.PublicIconURL) {
		return domain.NewError(400, "icon_url_invalid", "公开图标须为 HTTPS URL")
	}
	showcase := app.Showcase
	if len(showcase.Screenshots) > 8 || len(showcase.Highlights) > 12 || len(showcase.Permissions) > 12 {
		return domain.NewError(400, "showcase_limit", "截图最多 8 张，功能与权限最多各 12 条")
	}
	for _, address := range append(append([]string{}, showcase.Screenshots...), showcase.WebsiteURL, showcase.DocsURL, showcase.SourceURL) {
		if address != "" && !validEditorialURL(address) {
			return domain.NewError(400, "showcase_url_invalid", "展示图片与外部链接须为 HTTPS URL")
		}
	}
	for _, highlight := range showcase.Highlights {
		if strings.TrimSpace(highlight) == "" {
			return domain.NewError(400, "showcase_highlight_invalid", "功能亮点不能为空")
		}
	}
	for _, screenshot := range showcase.Screenshots {
		if strings.TrimSpace(screenshot) == "" {
			return domain.NewError(400, "showcase_image_invalid", "截图 URL 不能为空")
		}
	}
	return nil
}

// PublicationReadiness is shared by the Hub release action and the Studio
// publishing workflow. It checks editorial facts before a fixed OCI version
// can become visible in the Station catalog.
type PublicationReadiness struct {
	AppID         string   `json:"appId"`
	Revision      int      `json:"revision"`
	MissingFields []string `json:"missingFields"`
	Ready         bool     `json:"ready"`
}

func publicationReadiness(app domain.App) PublicationReadiness {
	missing := make([]string, 0, 7)
	if n := utf8.RuneCountInString(strings.TrimSpace(app.Name)); n < 2 || n > 128 {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(app.Category) == "" {
		missing = append(missing, "category")
	}
	if strings.TrimSpace(app.Summary) == "" {
		missing = append(missing, "summary")
	}
	if strings.TrimSpace(app.Developer) == "" {
		missing = append(missing, "developer")
	}
	if strings.TrimSpace(app.Description) == "" {
		missing = append(missing, "description")
	}
	if len(app.Showcase.Screenshots) == 0 {
		missing = append(missing, "showcase.screenshots")
	}
	if len(app.Showcase.Highlights) == 0 {
		missing = append(missing, "showcase.highlights")
	}
	return PublicationReadiness{AppID: app.ID, Revision: app.Revision, MissingFields: missing, Ready: len(missing) == 0}
}

func (s *Service) AppPublicationReadiness(ctx context.Context, subject, appID string) (PublicationReadiness, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return PublicationReadiness{}, err
	}
	managed, err := s.repository.ManagedApp(ctx, appID)
	if err != nil {
		return PublicationReadiness{}, err
	}
	return publicationReadiness(managed.App), nil
}
