package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestAPIKeyVerifierPersistsWithoutPublicExposure(t *testing.T) {
	m := NewMemorySeeded(time.Now().UTC())
	hash := sha256.Sum256([]byte("test-secret"))
	key, err := m.CreateAPIKey(context.Background(), "org_verdantflare", domain.APIKey{ID: "key_persistence", Name: "test", Prefix: "vf_live_••••", Scopes: []string{"models:read"}, Status: "有效", SecretHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("secretHash")) {
		t.Fatal("API response exposed verifier")
	}
	raw, err := encodeMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("secretHash")) {
		t.Fatal("repository omitted verifier")
	}
	reloaded, err := decodeMemory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.apiKeys[key.ID].SecretHash != hash {
		t.Fatal("API key verifier did not survive repository reload")
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	state["apiKeys"].(map[string]any)[key.ID].(map[string]any)["secretHash"] = nil
	legacyRaw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := decodeMemory(legacyRaw)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.apiKeys[key.ID].Status != "需重建" {
		t.Fatal("legacy key with missing verifier was not marked for replacement")
	}
}
