package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginHandoffSubjectClientChecksCurrentFact(t *testing.T) {
	const subject = "00000000-0000-4000-8000-000000000099"
	const token = "test-only-handoff-token-with-32-characters"
	status := http.StatusOK
	returnedSubject := subject
	checkedAt := time.Now().UTC()
	responseOverride := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/agent-handoff/v1/subjects/check" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("unexpected internal request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["subject_ref"] != subject || len(input) != 1 {
			t.Errorf("unexpected subject request: %+v %v", input, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			if responseOverride != "" {
				_, _ = w.Write([]byte(responseOverride))
				return
			}
			_ = json.NewEncoder(w).Encode(LoginSubjectFact{SubjectRef: returnedSubject, Active: true, CheckedAt: checkedAt})
		}
	}))
	defer server.Close()
	client, err := NewLoginHandoffClient(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := client.CheckSubject(context.Background(), subject)
	if err != nil || fact.SubjectRef != subject || !fact.Active {
		t.Fatalf("subject fact: %+v %v", fact, err)
	}
	status = http.StatusForbidden
	if _, err := client.CheckSubject(context.Background(), subject); err == nil || !strings.Contains(err.Error(), "主体不可用") {
		t.Fatalf("disabled subject not denied: %v", err)
	}
	status = http.StatusOK
	returnedSubject = "00000000-0000-4000-8000-000000000077"
	if _, err := client.CheckSubject(context.Background(), subject); err == nil {
		t.Fatal("mismatched Login subject accepted")
	}
	returnedSubject = subject
	checkedAt = time.Now().Add(-time.Minute)
	if _, err := client.CheckSubject(context.Background(), subject); err == nil {
		t.Fatal("stale Login fact accepted")
	}
	checkedAt = time.Now().UTC()
	responseOverride = `{"subject_ref":"` + subject + `","subject_ref":"` + subject + `","active":true,"checked_at":"` + checkedAt.Format(time.RFC3339Nano) + `"}`
	if _, err := client.CheckSubject(context.Background(), subject); err == nil {
		t.Fatal("duplicate Login response field accepted")
	}
	responseOverride = ""
	status = http.StatusServiceUnavailable
	if _, err := client.CheckSubject(context.Background(), subject); err == nil {
		t.Fatal("Login dependency failure accepted")
	}
}
