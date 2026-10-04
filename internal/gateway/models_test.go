package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsClientReadsTokenScopedModelIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer catalog-token" {
			t.Errorf("unexpected gateway request: path=%s auth=%t", r.URL.Path, r.Header.Get("Authorization") == "Bearer catalog-token")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"verdantflare-sd2"},{"id":"deepseek-flash"}]}`))
	}))
	defer server.Close()
	client, err := NewModelsClient(server.URL, "catalog-token")
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	if _, ok := models["verdantflare-sd2"]; !ok {
		t.Fatal("SD2 missing")
	}
	if _, ok := models["deepseek-flash"]; !ok {
		t.Fatal("DeepSeek missing")
	}
}

func TestModelsClientRejectsInvalidResponseAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("redirect") != "" {
			w.Header().Set("Location", "https://example.invalid/steal")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"data":[]}`))
	}))
	defer server.Close()
	client, err := NewModelsClient(server.URL, "catalog-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("invalid gateway response accepted")
	}
	client.url = server.URL + "/v1/models?redirect=1"
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
}

func TestModelsClientRejectsCredentialsInURL(t *testing.T) {
	if _, err := NewModelsClient("https://user:secret@api.example.com", "token"); err == nil {
		t.Fatal("URL credentials accepted")
	}
}
