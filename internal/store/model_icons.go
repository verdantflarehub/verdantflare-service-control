package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (m *Memory) PutModelProviderIcon(_ context.Context, icon domain.ModelProviderIcon) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelIcons == nil {
		m.modelIcons = make(map[string]domain.ModelProviderIcon)
	}
	icon.Data = append([]byte(nil), icon.Data...)
	m.modelIcons[icon.SHA256] = icon
	return nil
}

func (m *Memory) ModelProviderIcon(_ context.Context, digest string) (domain.ModelProviderIcon, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	icon, ok := m.modelIcons[digest]
	if !ok {
		return domain.ModelProviderIcon{}, domain.NewError(404, "model_icon_not_found", "模型图标不存在")
	}
	icon.Data = append([]byte(nil), icon.Data...)
	return icon, nil
}

func (p *Postgres) PutModelProviderIcon(ctx context.Context, icon domain.ModelProviderIcon) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO control_model_provider_icons (sha256, content_type, image_bytes, width, height)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (sha256) DO NOTHING`,
		icon.SHA256, icon.ContentType, icon.Data, icon.Width, icon.Height)
	if err != nil {
		return fmt.Errorf("persist model provider icon: %w", err)
	}
	return nil
}

func (p *Postgres) ModelProviderIcon(ctx context.Context, digest string) (domain.ModelProviderIcon, error) {
	var icon domain.ModelProviderIcon
	err := p.db.QueryRowContext(ctx, `
		SELECT sha256, content_type, image_bytes, width, height
		FROM control_model_provider_icons WHERE sha256=$1`, digest,
	).Scan(&icon.SHA256, &icon.ContentType, &icon.Data, &icon.Width, &icon.Height)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelProviderIcon{}, domain.NewError(404, "model_icon_not_found", "模型图标不存在")
	}
	if err != nil {
		return domain.ModelProviderIcon{}, fmt.Errorf("read model provider icon: %w", err)
	}
	return icon, nil
}
