package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/config"
)

func TestStudioPublicationDelegationRejectsUnsignedAndReplay(t *testing.T) {
	key := "test-only-key-with-at-least-thirty-two-characters"
	cfg := testConfig()
	cfg.StudioPublishHMACKey = key
	cfg.StudioPublishBindings = []config.StudioPublishBinding{{StationUserID: "station-user", StationOrganizationID: "station-org", LoginSubject: "logto_01vf9k2", ControlOrganizationID: "org_verdantflare"}}
	server := newTestServer(t, cfg)
	path := "/api/control/internal/studio-publish/mcp"
	requestBody := []byte(`{"jsonrpc":"2.0","id":"request-1","method":"tools/call","params":{"name":"app_publish.readiness","arguments":{"app_id":"missing-app"}}}`)
	unsigned, err := http.Post(server.URL+path, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, unsigned.Body)
	unsigned.Body.Close()
	if unsigned.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d", unsigned.StatusCode)
	}

	nonce := "0123456789abcdef0123456789abcdef"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(requestBody)
	message := fmt.Sprintf("POST\n%s\n%s\n%s\nstation-user\nstation-org\n%x", path, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(message))
	call := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-App-Publish-User", "station-user")
		req.Header.Set("X-App-Publish-Organization", "station-org")
		req.Header.Set("X-App-Publish-Timestamp", timestamp)
		req.Header.Set("X-App-Publish-Nonce", nonce)
		req.Header.Set("X-App-Publish-Signature", hex.EncodeToString(mac.Sum(nil)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := call()
	io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("signed status = %d", first.StatusCode)
	}
	replayed := call()
	io.Copy(io.Discard, replayed.Body)
	replayed.Body.Close()
	if replayed.StatusCode != http.StatusConflict {
		t.Fatalf("replayed status = %d", replayed.StatusCode)
	}
}
