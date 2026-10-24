package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelOpsClientSnapshot(t *testing.T) {
	const serviceToken = "model-ops-service-token-at-least-thirty-two-characters"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/center/model-ops/snapshot" || r.URL.Query().Get("modelId") != "minimax/h3" || r.Header.Get("Authorization") != "Bearer "+serviceToken {
			t.Errorf("unexpected model ops request: path=%q model=%q authorization=%t", r.URL.Path, r.URL.Query().Get("modelId"), r.Header.Get("Authorization") == "Bearer "+serviceToken)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"modelId":"minimax/h3","revision":"` + strings.Repeat("a", 64) + `","channels":[],"pricing":{},"metadata":{"exists":false}}}`))
	}))
	defer server.Close()
	client, err := NewModelOpsClient(server.URL, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background(), "minimax/h3")
	if err != nil || snapshot.ModelID != "minimax/h3" || len(snapshot.Revision) != 64 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
}

func TestModelOpsClientRejectsInvalidOriginAndCredential(t *testing.T) {
	for _, origin := range []string{"https://user:password@example.test", "https://example.test/path", "https://example.test?x=1"} {
		if _, err := NewModelOpsClient(origin, strings.Repeat("x", 32)); err == nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	if _, err := NewModelOpsClient("https://example.test", "short"); err == nil {
		t.Fatal("accepted short service token")
	}
}
