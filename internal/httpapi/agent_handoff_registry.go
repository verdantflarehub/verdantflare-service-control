package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
)

type handoffRegistry struct {
	Version   string            `json:"version"`
	Consumers []handoffConsumer `json:"consumers"`
}

type handoffConsumer struct {
	ClientURI         string         `json:"client_uri"`
	CertificateSHA256 []string       `json:"certificate_sha256"`
	RecipientRef      string         `json:"recipient_client_ref"`
	StationID         string         `json:"station_id"`
	Enabled           bool           `json:"enabled"`
	Routes            []handoffRoute `json:"routes"`
}

type handoffRoute struct {
	LocalOrgRef    string `json:"target_local_organization_ref"`
	ExternalOrgRef string `json:"external_organization_ref"`
	CallbackRef    string `json:"callback_ref"`
	CallbackURL    string `json:"callback_url"`
	BrowserOrigin  string `json:"browser_origin"`
}

func loadHandoffRegistry(path string) (handoffRegistry, error) {
	file, err := os.Open(path)
	if err != nil {
		return handoffRegistry{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return handoffRegistry{}, errors.New("handoff registry exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var registry handoffRegistry
	if err := decoder.Decode(&registry); err != nil {
		return handoffRegistry{}, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || registry.Version == "" || len(registry.Consumers) == 0 {
		return handoffRegistry{}, errors.New("invalid handoff registry")
	}
	seen := map[string]bool{}
	seenURIs := map[string]bool{}
	seenCertificates := map[string]bool{}
	for _, consumer := range registry.Consumers {
		uri, err := url.Parse(consumer.ClientURI)
		if err != nil || uri.Scheme != "spiffe" || uri.Host == "" || uri.Path == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || consumer.RecipientRef == "" || consumer.StationID == "" || len(consumer.CertificateSHA256) == 0 || len(consumer.Routes) == 0 {
			return handoffRegistry{}, errors.New("invalid handoff consumer")
		}
		key := consumer.RecipientRef + "|" + consumer.StationID
		if seen[key] || seenURIs[consumer.ClientURI] {
			return handoffRegistry{}, errors.New("duplicate handoff consumer")
		}
		seen[key] = true
		seenURIs[consumer.ClientURI] = true
		for _, digest := range consumer.CertificateSHA256 {
			if len(digest) != 64 {
				return handoffRegistry{}, errors.New("invalid certificate digest")
			}
			if _, err := hex.DecodeString(digest); err != nil {
				return handoffRegistry{}, err
			}
			digest = strings.ToLower(digest)
			if seenCertificates[digest] {
				return handoffRegistry{}, errors.New("duplicate handoff certificate")
			}
			seenCertificates[digest] = true
		}
		routes := map[string]bool{}
		for _, route := range consumer.Routes {
			if route.LocalOrgRef == "" || route.ExternalOrgRef == "" || route.CallbackRef == "" || routes[route.LocalOrgRef+"|"+route.CallbackRef] {
				return handoffRegistry{}, errors.New("invalid handoff route")
			}
			routes[route.LocalOrgRef+"|"+route.CallbackRef] = true
			callback, err := url.Parse(route.CallbackURL)
			if err != nil || callback.Scheme != "https" || callback.Host == "" || callback.User != nil || callback.Fragment != "" || callback.RawQuery != "" {
				return handoffRegistry{}, errors.New("invalid handoff callback")
			}
			browser, err := url.Parse(route.BrowserOrigin)
			if err != nil || browser.Scheme != "https" || browser.Host == "" || browser.User != nil || browser.Path != "" || browser.RawQuery != "" || browser.Fragment != "" {
				return handoffRegistry{}, errors.New("invalid handoff browser origin")
			}
		}
	}
	return registry, nil
}

func (r handoffRegistry) route(recipient, station, localOrg, externalOrg, callbackRef string) (handoffConsumer, handoffRoute, bool) {
	for _, consumer := range r.Consumers {
		if !consumer.Enabled || consumer.RecipientRef != recipient || consumer.StationID != station {
			continue
		}
		for _, route := range consumer.Routes {
			if route.LocalOrgRef == localOrg && route.ExternalOrgRef == externalOrg && route.CallbackRef == callbackRef {
				return consumer, route, true
			}
		}
	}
	return handoffConsumer{}, handoffRoute{}, false
}

func handoffCertificateAllowed(consumer handoffConsumer, uri string, rawCert []byte) bool {
	if !consumer.Enabled || consumer.ClientURI != uri || len(rawCert) == 0 {
		return false
	}
	digest := sha256.Sum256(rawCert)
	actual := hex.EncodeToString(digest[:])
	for _, allowed := range consumer.CertificateSHA256 {
		if strings.EqualFold(allowed, actual) {
			return true
		}
	}
	return false
}
