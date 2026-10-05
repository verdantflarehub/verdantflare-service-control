package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCenterClientUsesAuthenticatedOrganizationPaths(t *testing.T) {
	const secret = "service-token-with-at-least-thirty-two-characters"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("service token missing")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := request.Method + " " + request.URL.Path
		seen[key] = true
		writer.Header().Set("Content-Type", "application/json")
		switch key {
		case "GET /api/internal/center/organizations/org_alpha":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"remainingQuota":500000,"usedQuota":0,"enabled":true}}`))
		case "POST /api/internal/center/organizations/org_alpha/credit-grants":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"remainingQuota":1000000,"usedQuota":0,"enabled":true}}`))
		case "GET /api/internal/center/organizations/org_alpha/keys":
			_, _ = writer.Write([]byte(`{"success":true,"data":[{"id":1,"name":"test","status":"active"}]}`))
		case "POST /api/internal/center/organizations/org_alpha/keys":
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":1,"name":"test","secret":"sk-one-time","models":["deepseek-flash"]}}`))
		case "GET /api/internal/center/organizations/org_alpha/keys/1/probe":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"ok":true,"reason":"ok","models":["deepseek-flash"],"readOnly":true}}`))
		case "POST /api/internal/center/organizations/org_alpha/experience/chat-completions":
			var payload struct {
				ModelID string `json:"modelId"`
				Prompt  string `json:"prompt"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.ModelID != "deepseek-v4-pro" || payload.Prompt != "你好" {
				t.Errorf("unexpected experience request: %+v %v", payload, err)
			}
			writer.Header().Set("X-Oneapi-Request-Id", "202610051016420000000000000001")
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"已生成的回答"}}],"usage":{"prompt_tokens":8,"completion_tokens":12,"total_tokens":20}}`))
		case "GET /api/internal/center/organizations/org_alpha/experience/charges/202610051016420000000000000001":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"billedQuota":362}}`))
		case "POST /api/internal/center/organizations/org_alpha/keys/1/revoke", "PUT /api/internal/center/organizations/org_alpha/status":
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected request %s", key)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewCenterClient(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if balance, err := client.Balance(ctx, "org_alpha"); err != nil || balance.RemainingQuota != 500000 {
		t.Fatalf("balance: %+v %v", balance, err)
	}
	if balance, err := client.Grant(ctx, "org_alpha", "request_id_1234567890", "admin", 100); err != nil || balance.RemainingQuota != 1000000 {
		t.Fatalf("grant: %+v %v", balance, err)
	}
	if keys, err := client.ListKeys(ctx, "org_alpha"); err != nil || len(keys) != 1 {
		t.Fatalf("keys: %+v %v", keys, err)
	}
	if key, err := client.CreateKey(ctx, "org_alpha", "request_id_1234567890", "test", []string{"deepseek-flash"}, 30); err != nil || key.Secret != "sk-one-time" {
		t.Fatalf("create: %+v %v", key, err)
	}
	if probe, err := client.ProbeKey(ctx, "org_alpha", 1); err != nil || !probe.OK || !probe.ReadOnly {
		t.Fatalf("probe: %+v %v", probe, err)
	}
	if err := client.RevokeKey(ctx, "org_alpha", 1); err != nil {
		t.Fatal(err)
	}
	if err := client.SetEnabled(ctx, "org_alpha", false); err != nil {
		t.Fatal(err)
	}
	if result, err := client.ExperienceChat(ctx, "org_alpha", "deepseek-v4-pro", "你好"); err != nil || result.Response != "已生成的回答" || result.TotalTokens != 20 || result.BilledQuota == nil || *result.BilledQuota != 362 {
		t.Fatalf("experience chat: %+v %v", result, err)
	}
	if len(seen) != 9 {
		t.Fatalf("missing bridge calls: %v", seen)
	}
}

func TestCenterClientDoesNotInventChargeWhenLogIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			writer.Header().Set("X-Oneapi-Request-Id", "202610051016420000000000000002")
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"真实回答"}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := NewCenterClient(server.URL, "service-token-with-at-least-thirty-two-characters")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ExperienceChat(context.Background(), "org_alpha", "deepseek-flash", "你好")
	if err != nil || result.Response != "真实回答" || result.BilledQuota != nil {
		t.Fatalf("missing charge must not be estimated: %+v %v", result, err)
	}
}

func TestCenterClientRejectsCredentialURLAndRedirect(t *testing.T) {
	if _, err := NewCenterClient("https://user:password@example.test", "service-token-with-at-least-thirty-two-characters"); err == nil {
		t.Fatal("accepted URL credentials")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://example.test/steal")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client, err := NewCenterClient(server.URL, "service-token-with-at-least-thirty-two-characters")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Balance(context.Background(), "org_alpha"); err == nil {
		t.Fatal("accepted redirect")
	}
}
