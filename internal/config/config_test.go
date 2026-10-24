package config

import (
	"strings"
	"testing"
)

func TestProductionRejectsDevelopmentSubject(t *testing.T) {
	t.Setenv("CONTROL_ENV", "production")
	t.Setenv("CONTROL_DEV_LOGIN_SUBJECT", "logto_dev")
	t.Setenv("CONTROL_TRUST_AUTH_HEADERS", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected production configuration to reject development login subject")
	}
}

func TestProductionRequiresTrustedAuthBoundary(t *testing.T) {
	t.Setenv("CONTROL_ENV", "production")
	t.Setenv("CONTROL_DEV_LOGIN_SUBJECT", "")
	t.Setenv("CONTROL_TRUST_AUTH_HEADERS", "false")
	if _, err := Load(); err == nil {
		t.Fatal("expected production configuration to require trusted auth headers")
	}
}

func TestProductionRejectsInsecureGatewayURL(t *testing.T) {
	t.Setenv("CONTROL_ENV", "production")
	t.Setenv("CONTROL_DEV_LOGIN_SUBJECT", "")
	t.Setenv("CONTROL_TRUST_AUTH_HEADERS", "true")
	t.Setenv("CONTROL_DATABASE_URL", "postgres://localhost/control")
	t.Setenv("CONTROL_GATEWAY_BASE_URL", "HTTP://example.test")
	t.Setenv("CONTROL_GATEWAY_TOKEN", "test-token")
	if _, err := Load(); err == nil {
		t.Fatal("expected insecure gateway URL to be rejected")
	}
}

func TestPartialGatewayConfigurationRejected(t *testing.T) {
	t.Setenv("CONTROL_ENV", "development")
	t.Setenv("CONTROL_GATEWAY_BASE_URL", "https://example.test")
	t.Setenv("CONTROL_GATEWAY_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial gateway configuration to be rejected")
	}
}

func TestModelOpsCredentialIsDistinctAndRequiresGateway(t *testing.T) {
	serviceToken := strings.Repeat("m", 32)
	t.Setenv("CONTROL_ENV", "development")
	t.Setenv("CONTROL_GATEWAY_BASE_URL", "")
	t.Setenv("CONTROL_GATEWAY_TOKEN", "")
	t.Setenv("CONTROL_GATEWAY_MODEL_OPS_TOKEN", serviceToken)
	if _, err := Load(); err == nil {
		t.Fatal("model operations token without gateway origin accepted")
	}
	t.Setenv("CONTROL_GATEWAY_BASE_URL", "https://example.test")
	t.Setenv("CONTROL_GATEWAY_TOKEN", serviceToken)
	if _, err := Load(); err == nil {
		t.Fatal("model operations token reused client token")
	}
	t.Setenv("CONTROL_GATEWAY_TOKEN", "client-token")
	t.Setenv("CONTROL_GATEWAY_ADMIN_TOKEN", serviceToken)
	if _, err := Load(); err == nil {
		t.Fatal("model operations token reused organization token")
	}
	t.Setenv("CONTROL_GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 32))
	configuration, err := Load()
	if err != nil || configuration.GatewayModelOpsToken != serviceToken {
		t.Fatalf("distinct model operations token rejected: %v", err)
	}
}

func TestStationMTLSRequiresCompleteSeparateListener(t *testing.T) {
	t.Setenv("CONTROL_ENV", "development")
	t.Setenv("CONTROL_STATION_MTLS_ADDRESS", "127.0.0.1:8443")
	if _, err := Load(); err == nil {
		t.Fatal("partial Station mTLS configuration accepted")
	}
	t.Setenv("CONTROL_STATION_MTLS_CA_FILE", "/run/secrets/station-ca.pem")
	t.Setenv("CONTROL_STATION_MTLS_CERT_FILE", "/run/secrets/control.pem")
	t.Setenv("CONTROL_STATION_MTLS_KEY_FILE", "/run/secrets/control-key.pem")
	if _, err := Load(); err != nil {
		t.Fatalf("complete Station mTLS configuration rejected: %v", err)
	}
	t.Setenv("CONTROL_STATION_MTLS_ADDRESS", ":8080")
	if _, err := Load(); err == nil {
		t.Fatal("Station mTLS listener shared the browser address")
	}
}

func TestInstallGrantPolicyIsParsed(t *testing.T) {
	t.Setenv("CONTROL_ENV", "development")
	t.Setenv("CONTROL_INSTALL_GRANT_REQUIRED_APPS", "private-admin,internal-tool")
	configuration, err := Load()
	if err != nil || len(configuration.RequiredInstallGrantApps) != 2 {
		t.Fatalf("configured policy = %+v, %v", configuration.RequiredInstallGrantApps, err)
	}
}
