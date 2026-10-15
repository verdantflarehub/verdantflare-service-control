package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/httpapi"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestSurveyHTTPGuestFlowAndAuthentication(t *testing.T) {
	repo := store.NewMemoryBootstrap()
	server := httpapi.New(config.Config{Environment: "production", TrustAuthHeaders: true, AllowedOrigins: []string{"https://hub.example.test"}}, control.NewService(repo), slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := "/api/control/public/surveys/" + domain.SurveyID
	var cookie *http.Cookie
	do := func(method, path, body, origin, subject string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://hub.example.test"+path, strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("Content-Type", "application/json")
		if subject != "" {
			req.Header.Set("X-VF-Login-Subject", subject)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		return w
	}
	assertStatus := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	admin := "00000000-0000-4000-8000-000000000001"
	settingsPath := "/api/control/ops/surveys/" + domain.SurveyID + "/settings"
	assertStatus(do("GET", base, "", "", ""), 423)
	assertStatus(do("GET", base+"/captcha", "", "", ""), 423)
	assertStatus(do("PUT", settingsPath, `{"open":true,"expectedVersion":0}`, "https://hub.example.test", ""), 401)
	assertStatus(do("PUT", settingsPath, `{"open":true,"expectedVersion":0}`, "https://hub.example.test", "unknown"), 403)
	assertStatus(do("PUT", settingsPath, `{"open":true,"expectedVersion":0}`, "https://evil.example", admin), 403)
	assertStatus(do("PUT", settingsPath, `{"open":true}`, "https://hub.example.test", admin), 400)
	assertStatus(do("PUT", settingsPath, `{"open":true,"expectedVersion":0}`, "https://hub.example.test", admin), 200)
	assertStatus(do("PUT", settingsPath, `{"open":false,"expectedVersion":0}`, "https://hub.example.test", admin), 409)
	assertStatus(do("GET", base, "", "", ""), 401)
	assertStatus(do("GET", "/api/control/public/surveys/unknown/captcha", "", "", ""), 404)
	assertStatus(do("GET", "/api/control/ops/surveys/"+domain.SurveyID+"/stats", "", "", ""), 401)
	challenge := strings.Repeat("c", 64)
	hash := sha256.Sum256([]byte(challenge + ":123456"))
	if err := repo.UpdateSurvey(context.Background(), func(state *domain.SurveyState) error {
		state.Challenges[challenge] = domain.SurveyChallenge{AnswerHash: hex.EncodeToString(hash[:]), ExpiresAt: time.Now().Add(time.Minute)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"nickname":"测试访客","challengeId":"` + challenge + `","code":"123456"}`
	assertStatus(do("POST", base+"/session", body, "https://evil.example", ""), 403)
	assertStatus(do("POST", base+"/session", body, "", ""), 403)
	w := do("POST", base+"/session", body, "https://hub.example.test", "")
	assertStatus(w, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	cookie = cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/control/public/surveys/" {
		t.Fatal("unsafe cookie flags")
	}
	assertStatus(do("GET", base, "", "", ""), 200)
	assertStatus(do("POST", base+"/responses", `{"version":1,"unknown":true}`, "https://hub.example.test", ""), 400)
	assertStatus(do("POST", base+"/responses", `{"answers":{"q4":{"text":"`+strings.Repeat("x", 66000)+`"}}}`, "https://hub.example.test", ""), 400)
	submission := `{"version":1,"analysis":"yes","quote":"no","contactPermission":"no","contact":"","answers":{"q1":{"choices":["技术运维"]}}}`
	assertStatus(do("PUT", settingsPath, `{"open":false,"expectedVersion":1}`, "https://hub.example.test", admin), 200)
	assertStatus(do("POST", base+"/responses", submission, "https://hub.example.test", ""), 423)
	assertStatus(do("POST", base+"/session", body, "https://hub.example.test", ""), 423)
	assertStatus(do("GET", base, "", "", ""), 423)
	assertStatus(do("PUT", settingsPath, `{"open":true,"expectedVersion":2}`, "https://hub.example.test", admin), 200)
	w = do("POST", base+"/responses", submission, "https://hub.example.test", "")
	assertStatus(w, 200)
	var first map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	w = do("POST", base+"/responses", submission, "https://hub.example.test", "")
	assertStatus(w, 200)
	var second map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if first["responseId"] == "" || first["responseId"] != second["responseId"] {
		t.Fatal("not idempotent")
	}
	assertStatus(do("PUT", settingsPath, `{"open":false,"expectedVersion":3}`, "https://hub.example.test", admin), 200)
	assertStatus(do("GET", "/api/control/ops/surveys/"+domain.SurveyID+"/stats", "", "", ""), 401)
	assertStatus(do("GET", "/api/control/ops/surveys/"+domain.SurveyID+"/stats", "", "", "unknown"), 403)
	assertStatus(do("GET", "/api/control/ops/surveys/"+domain.SurveyID+"/stats", "", "", "00000000-0000-4000-8000-000000000001"), 200)
	w = do("GET", base, "", "", "")
	assertStatus(w, 200)
	if strings.Contains(w.Body.String(), "answerHash") || strings.Contains(w.Body.String(), "expiresAt") || strings.Contains(w.Body.String(), "contactPermission") || strings.Contains(w.Body.String(), "updatedBy") {
		t.Fatal("private repository state leaked")
	}
}
