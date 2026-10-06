package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const internalTestOrganizationID = "org_verdantflare"

var stationUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var certificateFingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type CreateStationTestGrantInput struct {
	StationID         string    `json:"stationId"`
	OrganizationID    string    `json:"organizationId"`
	CertificateSHA256 string    `json:"certificateSha256"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type StationIdentity struct {
	StationID         string
	CertificateSHA256 string
}

type StationCandidate struct {
	AppID          string                 `json:"appId"`
	Version        string                 `json:"version"`
	ManifestSHA256 string                 `json:"manifestSha256"`
	ManifestPath   string                 `json:"manifestPath"`
	Artifacts      []domain.AppArtifact   `json:"artifacts"`
	Dependencies   []domain.AppDependency `json:"dependencies"`
	Permissions    []domain.AppPermission `json:"permissions"`
	Status         string                 `json:"status"`
	Installable    bool                   `json:"installable"`
	GrantRevision  int                    `json:"grantRevision"`
	ExpiresAt      time.Time              `json:"expiresAt"`
}

type StationCandidateCatalog struct {
	StationID      string             `json:"stationId"`
	OrganizationID string             `json:"organizationId"`
	Items          []StationCandidate `json:"items"`
}

func (s *Service) CreateStationTestGrant(ctx context.Context, subject, appID, version string, input CreateStationTestGrantInput) (domain.StationTestGrant, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.StationTestGrant{}, err
	}
	now := time.Now().UTC()
	if !appIDPattern.MatchString(appID) || !candidateVersionPattern.MatchString(version) ||
		!stationUUIDPattern.MatchString(input.StationID) || input.OrganizationID != internalTestOrganizationID ||
		!certificateFingerprintPattern.MatchString(input.CertificateSHA256) ||
		!input.ExpiresAt.After(now) || input.ExpiresAt.After(now.Add(30*24*time.Hour)) {
		return domain.StationTestGrant{}, domain.NewError(400, "station_grant_invalid", "仅可为内部组织登记 30 天内有效的 Station ID、证书指纹和候选版本")
	}
	if _, err := s.repository.Organization(ctx, input.OrganizationID); err != nil {
		return domain.StationTestGrant{}, err
	}
	if _, err := s.repository.AppVersion(ctx, appID, version); err != nil {
		return domain.StationTestGrant{}, err
	}
	return s.repository.CreateStationTestGrant(ctx, domain.StationTestGrant{
		StationID: input.StationID, OrganizationID: input.OrganizationID, AppID: appID, Version: version,
		CertificateSHA256: input.CertificateSHA256, ExpiresAt: input.ExpiresAt.UTC(), CreatedAt: now,
	})
}

func (s *Service) ListStationTestGrants(ctx context.Context, subject, appID string) ([]domain.StationTestGrant, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return nil, err
	}
	return s.repository.ListStationTestGrants(ctx, appID)
}

func (s *Service) RevokeStationTestGrant(ctx context.Context, subject, appID, version, stationID string) (domain.StationTestGrant, error) {
	if err := s.requireOperationsRole(ctx, subject, "app_ops_admin"); err != nil {
		return domain.StationTestGrant{}, err
	}
	return s.repository.RevokeStationTestGrant(ctx, appID, version, stationID)
}

func authorizedGrant(grants []domain.StationTestGrant, identity StationIdentity, appID, version string, now time.Time) (domain.StationTestGrant, bool) {
	for _, grant := range grants {
		if grant.AppID == appID && grant.Version == version && stationGrantMatches(grant, identity, now) {
			return grant, true
		}
	}
	return domain.StationTestGrant{}, false
}

func stationGrantMatches(grant domain.StationTestGrant, identity StationIdentity, now time.Time) bool {
	return grant.StationID == identity.StationID && grant.OrganizationID == internalTestOrganizationID &&
		!grant.Revoked && now.Before(grant.ExpiresAt) &&
		subtle.ConstantTimeCompare([]byte(grant.CertificateSHA256), []byte(identity.CertificateSHA256)) == 1
}

func (s *Service) StationCandidateCatalog(ctx context.Context, identity StationIdentity) (StationCandidateCatalog, error) {
	result := StationCandidateCatalog{StationID: identity.StationID, OrganizationID: internalTestOrganizationID, Items: []StationCandidate{}}
	grants, err := s.repository.StationTestGrantsForStation(ctx, identity.StationID)
	if err != nil {
		return StationCandidateCatalog{}, err
	}
	now := time.Now().UTC()
	for _, candidate := range grants {
		if !stationGrantMatches(candidate, identity, now) {
			continue
		}
		version, err := s.repository.AppVersion(ctx, candidate.AppID, candidate.Version)
		if err != nil {
			return StationCandidateCatalog{}, err
		}
		if !manifestDigestValid(version) {
			return StationCandidateCatalog{}, domain.NewError(503, "manifest_integrity_failed", "候选 Manifest 摘要校验失败")
		}
		result.Items = append(result.Items, StationCandidate{
			AppID: version.AppID, Version: version.Version, ManifestSHA256: version.ManifestSHA256,
			ManifestPath: "/api/control/station/v1/apps/" + version.AppID + "/versions/" + version.Version + "/manifest",
			Artifacts:    version.Artifacts, Dependencies: version.Dependencies, Permissions: version.Permissions,
			Status: version.Status, Installable: false, GrantRevision: candidate.Revision, ExpiresAt: candidate.ExpiresAt,
		})
	}
	return result, nil
}

func manifestDigestValid(version domain.AppVersion) bool {
	digest := sha256.Sum256(version.Manifest)
	return hex.EncodeToString(digest[:]) == version.ManifestSHA256 && json.Valid(version.Manifest)
}

func (s *Service) StationCandidateManifest(ctx context.Context, identity StationIdentity, appID, versionID string) (json.RawMessage, string, error) {
	grants, err := s.repository.StationTestGrantsForStation(ctx, identity.StationID)
	if err != nil {
		return nil, "", err
	}
	if _, ok := authorizedGrant(grants, identity, appID, versionID, time.Now().UTC()); !ok {
		return nil, "", domain.NewError(404, "station_candidate_not_found", "候选版本不存在")
	}
	version, err := s.repository.AppVersion(ctx, appID, versionID)
	if err != nil {
		return nil, "", err
	}
	if !manifestDigestValid(version) {
		return nil, "", domain.NewError(503, "manifest_integrity_failed", "候选 Manifest 摘要校验失败")
	}
	return version.Manifest, version.ManifestSHA256, nil
}
