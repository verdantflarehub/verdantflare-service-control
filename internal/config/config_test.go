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
