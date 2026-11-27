package skillsource_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource"
	"github.com/verdantflarehub/verdantflare-service-control/internal/skillsource/skillsourcetest"
)

var commit = strings.Repeat("c", 40)

func client(t *testing.T, g *skillsourcetest.GitHub, token string) *skillsource.Client {
	t.Helper()
	c, err := skillsource.New(g.API.URL, skillsourcetest.Repository, token)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func status(err error) (int, string) {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Status, failure.Code
	}
	return 0, ""
}

func TestFetchTrustedRelease(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		g := skillsourcetest.New(t, "read-only-token")
		archive := []byte("zip bytes")
		g.Put("0.3.0", skillsourcetest.Release{Archive: archive, Commit: commit, Annotated: annotated})
		data, source, err := client(t, g, "read-only-token").Fetch(context.Background(), "0.3.0", skillsourcetest.SHA256(archive))
		if err != nil {
			t.Fatalf("annotated=%v: %v", annotated, err)
		}
		if string(data) != string(archive) || source.Commit != commit || source.Tag != "vf-hub-skill-input-0.3.0" ||
			source.Repository != skillsourcetest.Repository || source.Uploader != "github-actions[bot]" || source.Size != int64(len(archive)) || source.AssetID == 0 {
			t.Fatalf("source=%+v", source)
		}
		if g.StorageSawAuthorization {
			t.Fatal("the token was forwarded to the asset storage host")
		}
	}
}

func TestFetchRefusesUntrustedReleases(t *testing.T) {
	archive := []byte("zip bytes")
	digest := skillsourcetest.SHA256(archive)
	cases := []struct {
		name    string
		release skillsourcetest.Release
		digest  string
		status  int
		code    string
	}{
		{"manual release", skillsourcetest.Release{Author: "someone"}, digest, 422, "skill_source_untrusted"},
		{"manual asset", skillsourcetest.Release{Uploader: "someone"}, digest, 422, "skill_source_untrusted"},
		{"renamed asset", skillsourcetest.Release{AssetName: "other.zip"}, digest, 422, "skill_source_untrusted"},
		{"extra asset", skillsourcetest.Release{ExtraAsset: true}, digest, 422, "skill_source_untrusted"},
		{"draft", skillsourcetest.Release{Draft: true}, digest, 422, "skill_source_untrusted"},
		{"commit not on release", skillsourcetest.Release{CompareState: "ahead"}, digest, 422, "skill_source_untrusted"},
		{"diverged commit", skillsourcetest.Release{CompareState: "diverged"}, digest, 422, "skill_source_untrusted"},
		{"oversized", skillsourcetest.Release{DeclaredSize: skillsource.MaxArchiveBytes + 1}, digest, 422, "skill_source_untrusted"},
		{"short download", skillsourcetest.Release{DeclaredSize: int64(len(archive)) + 5}, digest, 422, "skill_source_untrusted"},
		{"wrong digest", skillsourcetest.Release{}, strings.Repeat("0", 64), 409, "skill_source_digest_mismatch"},
	}
	for _, tc := range cases {
		g := skillsourcetest.New(t, "")
		tc.release.Archive, tc.release.Commit = archive, commit
		g.Put("0.3.0", tc.release)
		_, _, err := client(t, g, "").Fetch(context.Background(), "0.3.0", tc.digest)
		if got, code := status(err); got != tc.status || code != tc.code {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
}

func TestFetchMissingReleaseAndUpstreamFailure(t *testing.T) {
	g := skillsourcetest.New(t, "")
	if got, code := status(func() error {
		_, _, err := client(t, g, "").Fetch(context.Background(), "9.9.9", strings.Repeat("0", 64))
		return err
	}()); got != 404 || code != "skill_source_not_found" {
		t.Fatalf("missing release: %d %s", got, code)
	}
	g.Put("0.3.0", skillsourcetest.Release{Archive: []byte("x"), Commit: commit})
	g.Token = "required"
	if got, _ := status(func() error {
		_, _, err := client(t, g, "wrong").Fetch(context.Background(), "0.3.0", skillsourcetest.SHA256([]byte("x")))
		return err
	}()); got != 502 {
		t.Fatalf("rejected credentials should be an upstream failure, got %d", got)
	}
	g.API.Close()
	if got, code := status(func() error {
		_, _, err := client(t, g, "").Fetch(context.Background(), "0.3.0", skillsourcetest.SHA256([]byte("x")))
		return err
	}()); got != 502 || code != "skill_source_unavailable" {
		t.Fatalf("closed API: %d %s", got, code)
	}
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	for _, tc := range [][2]string{{"ftp://example.com", skillsourcetest.Repository}, {"https://api.github.com", "not a repo"}, {"https://api.github.com", "a/b/c"}} {
		if _, err := skillsource.New(tc[0], tc[1], ""); err == nil {
			t.Errorf("accepted %v", tc)
		}
	}
}
