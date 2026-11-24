package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const directoryRequestDeadline = 900 * time.Millisecond
const directoryRequestsPerSecond = 20

var directorySubjectPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func directoryRevisionValid(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func (p *AgentHandoffProvider) directoryRateAllowed(consumer handoffConsumer) bool {
	p.directoryMu.Lock()
	defer p.directoryMu.Unlock()
	key := consumer.RecipientRef + "|" + consumer.StationID
	now := time.Now()
	window := p.directoryUse[key]
	if now.Sub(window.Started) >= time.Second {
		window = directoryRateWindow{Started: now}
	}
	if window.Count >= directoryRequestsPerSecond {
		return false
	}
	window.Count++
	p.directoryUse[key] = window
	return true
}

func directoryFailure(w http.ResponseWriter, err error) {
	var problem *domain.Error
	if errors.As(err, &problem) {
		switch problem.Status {
		case http.StatusForbidden:
			handoffError(w, handoffForbidden())
			return
		case http.StatusConflict:
			handoffError(w, domain.NewError(409, "HANDOFF_REVISION_CHANGED", "身份目录已更新"))
			return
		}
	}
	handoffError(w, handoffUnavailable())
}

func (p *AgentHandoffProvider) currentDirectoryConsumer(r *http.Request, previous handoffConsumer, localOrg, externalOrg string) error {
	registry, err := loadHandoffRegistry(p.registryFile)
	if err != nil {
		return handoffUnavailable()
	}
	cert := r.TLS.PeerCertificates[0]
	for _, consumer := range registry.Consumers {
		if consumer.RecipientRef != previous.RecipientRef || consumer.StationID != previous.StationID || consumer.ClientURI != previous.ClientURI || !handoffCertificateAllowed(consumer, cert.URIs[0].String(), cert.Raw) {
			continue
		}
		if localOrg == "" {
			return nil
		}
		current, ok := registry.directoryRoute(previous.RecipientRef, previous.StationID, localOrg, externalOrg)
		if ok && current.ClientURI == previous.ClientURI {
			return nil
		}
	}
	return handoffForbidden()
}

func (p *AgentHandoffProvider) directoryRevision(w http.ResponseWriter, r *http.Request) {
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	if !p.directoryRateAllowed(consumer) {
		handoffError(w, domain.NewError(429, "HANDOFF_RATE_LIMITED", "查询过于频繁"))
		return
	}
	var input handoffDirectoryRevisionRequest
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	if input.ProtocolVersion != 1 || input.ProviderRef != handoffProviderRef {
		handoffError(w, handoffInvalid())
		return
	}
	if input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID {
		handoffError(w, handoffForbidden())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryRequestDeadline)
	defer cancel()
	revision, checkedAt, err := p.repository.DirectoryRevision(ctx)
	if err != nil || ctx.Err() != nil || !directoryRevisionValid(revision) {
		handoffError(w, handoffUnavailable())
		return
	}
	if err := p.currentDirectoryConsumer(r, consumer, "", ""); err != nil {
		handoffError(w, err)
		return
	}
	handoffJSON(w, http.StatusOK, map[string]any{"protocol_version": 1, "identity_revision": revision, "checked_at": checkedAt})
}

func (p *AgentHandoffProvider) directoryMember(w http.ResponseWriter, r *http.Request) {
	consumer := r.Context().Value(handoffConsumerKey{}).(handoffConsumer)
	if !p.directoryRateAllowed(consumer) {
		handoffError(w, domain.NewError(429, "HANDOFF_RATE_LIMITED", "查询过于频繁"))
		return
	}
	var input handoffDirectoryMemberRequest
	if err := strictHandoffJSON(w, r, &input); err != nil {
		handoffError(w, err)
		return
	}
	if input.ProtocolVersion != 1 || input.ProviderRef != handoffProviderRef || !handoffIdentifier(input.LocalOrgRef) || !handoffIdentifier(input.ExternalOrgRef) || !directorySubjectPattern.MatchString(input.SubjectRef) || !directoryRevisionValid(input.ExpectedRevision) {
		handoffError(w, handoffInvalid())
		return
	}
	if input.RecipientRef != consumer.RecipientRef || input.StationID != consumer.StationID {
		handoffError(w, handoffForbidden())
		return
	}
	if err := p.currentDirectoryConsumer(r, consumer, input.LocalOrgRef, input.ExternalOrgRef); err != nil {
		handoffError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryRequestDeadline)
	defer cancel()
	first, err := p.repository.CheckDirectoryMember(ctx, input.SubjectRef, input.ExternalOrgRef, input.ExpectedRevision)
	if err != nil {
		directoryFailure(w, err)
		return
	}
	active, err := p.login.CheckSubject(ctx, input.SubjectRef)
	if err != nil {
		directoryFailure(w, err)
		return
	}
	if !active.Active || active.SubjectRef != input.SubjectRef {
		handoffError(w, handoffUnavailable())
		return
	}
	last, err := p.repository.CheckDirectoryMember(ctx, input.SubjectRef, input.ExternalOrgRef, input.ExpectedRevision)
	if err != nil {
		directoryFailure(w, err)
		return
	}
	if ctx.Err() != nil || first.CenterUserRef != last.CenterUserRef || first.IdentityRevision != last.IdentityRevision || last.SubjectRef != input.SubjectRef || last.ExternalOrgRef != input.ExternalOrgRef {
		handoffError(w, handoffUnavailable())
		return
	}
	if err := p.currentDirectoryConsumer(r, consumer, input.LocalOrgRef, input.ExternalOrgRef); err != nil {
		handoffError(w, err)
		return
	}
	handoffJSON(w, http.StatusOK, map[string]any{
		"protocol_version": 1, "provider_ref": handoffProviderRef, "recipient_client_ref": consumer.RecipientRef,
		"station_id": consumer.StationID, "target_local_organization_ref": input.LocalOrgRef,
		"subject_ref": last.SubjectRef, "center_user_ref": last.CenterUserRef,
		"external_organization_ref": last.ExternalOrgRef, "identity_revision": last.IdentityRevision,
		"checked_at": last.CheckedAt, "active": true,
	})
}
