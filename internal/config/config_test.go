package config

import "testing"

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
