package control

import (
	"context"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func (s *Service) ListCreativeResources(ctx context.Context, subject, mediaType, postKind string) ([]domain.CreativeResource, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return nil, err
	}
	return s.repository.ListCreativeResources(ctx, org.OrganizationID, mediaType, postKind)
}

func (s *Service) GetCreativeResource(ctx context.Context, subject, resourceID string) (domain.CreativeResource, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResource{}, err
	}
	return s.repository.GetCreativeResource(ctx, org.OrganizationID, resourceID)
}

func (s *Service) GetCreativeResourceImage(ctx context.Context, subject, resourceID string) (domain.CreativeResourceImage, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResourceImage{}, err
	}
	return s.repository.GetCreativeResourceImage(ctx, org.OrganizationID, resourceID)
}

func (s *Service) GetCreativeResourceVideo(ctx context.Context, subject, resourceID string) (domain.CreativeResourceImage, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResourceImage{}, err
	}
	return s.repository.GetCreativeResourceVideo(ctx, org.OrganizationID, resourceID)
}

func (s *Service) GetCreativeResourceItemImage(ctx context.Context, subject, resourceID, itemID string) (domain.CreativeResourceImage, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResourceImage{}, err
	}
	return s.repository.GetCreativeResourceItemImage(ctx, org.OrganizationID, resourceID, itemID)
}

func (s *Service) DeleteCreativeResourceItem(ctx context.Context, subject, resourceID, itemID string) (domain.CreativeResource, bool, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResource{}, false, err
	}
	return s.repository.DeleteCreativeResourceItem(ctx, org.OrganizationID, resourceID, itemID)
}

func (s *Service) WithdrawCreativeResource(ctx context.Context, subject, resourceID string) (domain.CreativeResource, error) {
	org, _, _, err := s.repository.ActiveOrganization(ctx, subject)
	if err != nil {
		return domain.CreativeResource{}, err
	}
	return s.repository.WithdrawCreativeResource(ctx, org.OrganizationID, resourceID)
}
