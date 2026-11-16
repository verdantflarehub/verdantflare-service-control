package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type AssistantMTLS struct{ Address, CAFile, CertFile, KeyFile, ClientURI string }

func loadAssistantMTLS(publicAddress, stationAddress, databaseURL string) (AssistantMTLS, error) {
	c := AssistantMTLS{
		Address:   strings.TrimSpace(os.Getenv("CONTROL_ASSISTANT_MTLS_ADDRESS")),
		CAFile:    strings.TrimSpace(os.Getenv("CONTROL_ASSISTANT_MTLS_CA_FILE")),
		CertFile:  strings.TrimSpace(os.Getenv("CONTROL_ASSISTANT_MTLS_CERT_FILE")),
		KeyFile:   strings.TrimSpace(os.Getenv("CONTROL_ASSISTANT_MTLS_KEY_FILE")),
		ClientURI: strings.TrimSpace(os.Getenv("CONTROL_ASSISTANT_MTLS_CLIENT_URI")),
	}
	count := 0
	for _, v := range []string{c.Address, c.CAFile, c.CertFile, c.KeyFile, c.ClientURI} {
		if v != "" {
			count++
		}
	}
	if count == 0 {
		return c, nil
	}
	if count != 5 || databaseURL == "" {
		return AssistantMTLS{}, fmt.Errorf("assistant mTLS requires all five fields and persistent database")
	}
	if c.Address == publicAddress || c.Address == stationAddress {
		return AssistantMTLS{}, fmt.Errorf("assistant mTLS must use a separate listener")
	}
	uri, err := url.Parse(c.ClientURI)
	if err != nil || uri.Scheme != "spiffe" || uri.Host == "" || uri.Path == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" {
		return AssistantMTLS{}, fmt.Errorf("assistant mTLS client URI is invalid")
	}
	return c, nil
}
