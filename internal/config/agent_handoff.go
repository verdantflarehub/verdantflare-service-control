package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type AgentHandoff struct {
	Address       string
	CAFile        string
	CertFile      string
	KeyFile       string
	RegistryFile  string
	Issuer        string
	LoginBaseURL  string
	LoginToken    string
	IdentityFloor uint64
	EpochFloor    uint64
}

func loadAgentHandoff(publicAddress, stationAddress, assistantAddress, databaseURL, directoryToken string) (AgentHandoff, error) {
	c := AgentHandoff{
		Address:      strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_MTLS_ADDRESS")),
		CAFile:       strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_MTLS_CA_FILE")),
		CertFile:     strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_MTLS_CERT_FILE")),
		KeyFile:      strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_MTLS_KEY_FILE")),
		RegistryFile: strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_REGISTRY_FILE")),
		Issuer:       strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_ISSUER")),
		LoginBaseURL: strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_LOGIN_BASE_URL")),
		LoginToken:   strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_LOGIN_TOKEN")),
	}
	identityFloor := strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_IDENTITY_FLOOR"))
	epochFloor := strings.TrimSpace(os.Getenv("CONTROL_HANDOFF_EPOCH_FLOOR"))
	count := 0
	for _, value := range []string{c.Address, c.CAFile, c.CertFile, c.KeyFile, c.RegistryFile, c.Issuer, c.LoginBaseURL, c.LoginToken, identityFloor, epochFloor} {
		if value != "" {
			count++
		}
	}
	if count == 0 {
		return c, nil
	}
	if count != 10 || databaseURL == "" || len(c.LoginToken) < 32 || c.LoginToken == directoryToken {
		return AgentHandoff{}, errors.New("agent handoff requires all fields, external high-water floors, persistent database and a distinct Login credential")
	}
	var err error
	c.IdentityFloor, err = strconv.ParseUint(identityFloor, 10, 64)
	if err != nil || c.IdentityFloor == 0 {
		return AgentHandoff{}, errors.New("agent handoff identity high-water floor is invalid")
	}
	c.EpochFloor, err = strconv.ParseUint(epochFloor, 10, 64)
	if err != nil || c.EpochFloor == 0 {
		return AgentHandoff{}, errors.New("agent handoff epoch high-water floor is invalid")
	}
	if c.Address == publicAddress || c.Address == stationAddress || c.Address == assistantAddress {
		return AgentHandoff{}, errors.New("agent handoff requires a separate mTLS listener")
	}
	issuer, err := url.Parse(c.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.Path != "" || issuer.RawQuery != "" || issuer.Fragment != "" {
		return AgentHandoff{}, errors.New("agent handoff issuer must be a fixed HTTPS origin")
	}
	login, err := url.Parse(c.LoginBaseURL)
	if err != nil || (login.Scheme != "http" && login.Scheme != "https") || login.Host == "" || login.User != nil || login.RawQuery != "" || login.Fragment != "" || login.Path != "" {
		return AgentHandoff{}, errors.New("agent handoff Login base URL is invalid")
	}
	return c, nil
}
