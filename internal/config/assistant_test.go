package config

import "testing"

func TestAssistantMTLSRequiresDedicatedPersistentConfiguration(t *testing.T) {
	keys := []string{"CONTROL_ASSISTANT_MTLS_ADDRESS", "CONTROL_ASSISTANT_MTLS_CA_FILE", "CONTROL_ASSISTANT_MTLS_CERT_FILE", "CONTROL_ASSISTANT_MTLS_KEY_FILE", "CONTROL_ASSISTANT_MTLS_CLIENT_URI"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	if _, err := loadAssistantMTLS(":8080", ":8443", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv(keys[0], ":9443")
	if _, err := loadAssistantMTLS(":8080", ":8443", "db"); err == nil {
		t.Fatal("partial configuration accepted")
	}
	for i, value := range []string{":9443", "ca", "cert", "key", "spiffe://verdantflare/assistant"} {
		t.Setenv(keys[i], value)
	}
	if _, err := loadAssistantMTLS(":8080", ":8443", ""); err == nil {
		t.Fatal("memory identity backend accepted")
	}
	if _, err := loadAssistantMTLS(":9443", ":8443", "db"); err == nil {
		t.Fatal("public listener reused")
	}
	if _, err := loadAssistantMTLS(":8080", ":9443", "db"); err == nil {
		t.Fatal("Station listener reused")
	}
	if _, err := loadAssistantMTLS(":8080", ":8443", "db"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(keys[4], "https://example.invalid/assistant")
	if _, err := loadAssistantMTLS(":8080", ":8443", "db"); err == nil {
		t.Fatal("invalid service identity accepted")
	}
}
