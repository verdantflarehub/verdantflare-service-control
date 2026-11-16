//go:build integration

package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func TestAssistantNodeMTLSPostgres(t *testing.T) {
	connection := os.Getenv("ASSISTANT_TEST_DATABASE_URL")
	u, err := url.Parse(connection)
	if err != nil || !strings.HasPrefix(u.Path, "/pa_test_") {
		t.Fatal("isolated pa_test_ database required")
	}
	node, probe := os.Getenv("ASSISTANT_TEST_NODE"), os.Getenv("ASSISTANT_TEST_PROBE")
	if node == "" || probe == "" {
		t.Fatal("compiled Node client probe required")
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migration, err := os.ReadFile("migrations/postgres/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	repo, err := store.OpenPostgres(connection, 4, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	seed, err := json.Marshal(map[string]any{
		"assistantIdentityRevision": "9007199254740993",
		"users": map[string]domain.CenterUser{"login-a": {
			CenterUserID: "owner-a", LoginSubject: "login-a", ActiveOrganizationID: "org-a",
			Memberships: []domain.Membership{{OrganizationID: "org-a", Roles: []string{"member"}, Status: "正常"}},
		}},
		"organizations": map[string]domain.Organization{"org-a": {OrganizationID: "org-a", Status: "正常"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the real bootstrap's non-identity maps; replace only identity fixtures.
	if _, err = db.Exec("UPDATE control_repository_state SET state=state || $1::jsonb WHERE singleton=TRUE", seed); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssistantIdentityReady(context.Background()); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	ca, caKey, caPEM, _ := assistantTestCert(t, nil, nil, true, "")
	_, _, serverPEM, serverKey := assistantTestCert(t, ca, caKey, false, "")
	_, _, clientPEM, clientKey := assistantTestCert(t, ca, caKey, false, "spiffe://verdantflare/assistant")
	_, _, wrongPEM, wrongKey := assistantTestCert(t, ca, caKey, false, "spiffe://verdantflare/station")
	otherCA, otherKey, otherPEM, _ := assistantTestCert(t, nil, nil, true, "")
	_, _, untrustedPEM, untrustedKey := assistantTestCert(t, otherCA, otherKey, false, "spiffe://verdantflare/assistant")
	for name, data := range map[string][]byte{"ca.pem": caPEM, "client.pem": clientPEM, "client.key": clientKey,
		"server.pem": serverPEM, "server.key": serverKey,
		"wrong.pem": wrongPEM, "wrong.key": wrongKey, "other-ca.pem": otherPEM,
		"untrusted.pem": untrustedPEM, "untrusted.key": untrustedKey} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	pair, err := tls.X509KeyPair(serverPEM, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	handler, err := NewAssistantIdentityHandler(repo, "spiffe://verdantflare/assistant")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	defer server.Close()

	// Optional composition mode runs the real Login test host while this real
	// mTLS Control endpoint remains alive. It is unavailable in production builds.
	if loginBinary := os.Getenv("ASSISTANT_TEST_LOGIN_BINARY"); loginBinary != "" {
		loginRoot := os.Getenv("ASSISTANT_TEST_LOGIN_ROOT")
		if loginRoot == "" {
			t.Fatal("isolated Login test root required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, loginBinary, "-test.run", "^TestAssistantNodeLoginSessionPostgres$", "-test.v")
		cmd.Dir = loginRoot
		cmd.Env = append(os.Environ(), "ASSISTANT_TLS_ORIGIN="+server.URL, "ASSISTANT_TLS_CERT_DIR="+dir, "ASSISTANT_LOGIN_COMPOSITION=1")
		output, e := cmd.CombinedOutput()
		t.Log(string(output))
		if e != nil {
			t.Fatalf("Login/Control composition failed: %v", e)
		}
		return
	}
	run := func(t *testing.T, phase string) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, node, probe)
		cmd.Env = append(os.Environ(), "ASSISTANT_TLS_ORIGIN="+server.URL, "ASSISTANT_TLS_CERT_DIR="+dir, "ASSISTANT_TLS_PHASE="+phase)
		output, e := cmd.CombinedOutput()
		t.Log(string(output))
		if e != nil {
			t.Fatalf("Node interoperability probe failed: %v", e)
		}
	}
	if !t.Run("valid-and-rejected-handshakes", func(t *testing.T) { run(t, "active") }) {
		return
	}
	if _, err = repo.UpdateManagedOrganization(context.Background(), "org-a", domain.Organization{OrganizationID: "org-a", Status: "冻结"}, nil, 0); err != nil {
		t.Fatal(err)
	}
	t.Run("persisted-freeze-denies-node-client", func(t *testing.T) { run(t, "frozen") })
}

func assistantTestCert(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey, isCA bool, identity string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "assistant-test-" + serial.Text(16)}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true, IsCA: isCA, KeyUsage: x509.KeyUsageDigitalSignature}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign
		issuer, issuerKey = template, key
	} else if identity != "" {
		uri, e := url.Parse(identity)
		if e != nil {
			t.Fatal(e)
		}
		template.URIs = []*url.URL{uri}
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
