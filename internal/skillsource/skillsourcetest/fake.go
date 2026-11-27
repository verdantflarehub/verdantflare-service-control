// Package skillsourcetest is a fake GitHub API for the skill source client:
// Releases, tags, branch comparison and asset downloads that redirect to a
// separate storage host, as github.com does.
package skillsourcetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const Repository = "verdantflarehub/verdantflare-skills"

// Release is one fake GitHub Release vf-hub-skill-input-<id>. Zero values
// describe a trusted release: workflow author and uploader, a lightweight
// tag, and a commit on the release branch.
type Release struct {
	Archive      []byte
	Commit       string
	Author       string // default github-actions[bot]
	Uploader     string // default github-actions[bot]
	AssetName    string // default <tag>.zip
	ExtraAsset   bool
	Draft        bool
	Annotated    bool
	CompareState string // default "behind"
	DeclaredSize int64  // default len(Archive)
}

type GitHub struct {
	API     *httptest.Server
	Storage *httptest.Server
	Token   string

	mu       sync.Mutex
	releases map[string]*Release // by tag
	// StorageSawAuthorization records whether any asset download reached
	// the storage host with an Authorization header.
	StorageSawAuthorization bool
	APIRequests             int
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// New starts the fake. token, when set, is required on every API call.
func New(t *testing.T, token string) *GitHub {
	t.Helper()
	g := &GitHub{Token: token, releases: map[string]*Release{}}
	storage := http.NewServeMux()
	storage.HandleFunc("GET /blob/{tag}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			g.StorageSawAuthorization = true
		}
		release := g.releases[r.PathValue("tag")]
		if release == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(release.Archive)
	})
	g.Storage = httptest.NewServer(storage)
	t.Cleanup(g.Storage.Close)

	api := http.NewServeMux()
	prefix := "/repos/" + Repository
	api.HandleFunc("GET "+prefix+"/releases/tags/{tag}", g.locked(func(w http.ResponseWriter, r *http.Request, tag string, release *Release) {
		uploader := or(release.Uploader, "github-actions[bot]")
		size := release.DeclaredSize
		if size == 0 {
			size = int64(len(release.Archive))
		}
		assets := []map[string]any{{"id": assetID(tag), "name": or(release.AssetName, tag+".zip"), "size": size, "state": "uploaded",
			"uploader": map[string]any{"login": uploader}}}
		if release.ExtraAsset {
			assets = append(assets, map[string]any{"id": 9, "name": "notes.txt", "size": 1, "state": "uploaded", "uploader": map[string]any{"login": uploader}})
		}
		writeJSON(w, map[string]any{"id": 7000 + assetID(tag), "tag_name": tag, "draft": release.Draft,
			"author": map[string]any{"login": or(release.Author, "github-actions[bot]")}, "assets": assets})
	}, "tag"))
	api.HandleFunc("GET "+prefix+"/git/ref/tags/{tag}", g.locked(func(w http.ResponseWriter, r *http.Request, tag string, release *Release) {
		if release.Annotated {
			writeJSON(w, map[string]any{"object": map[string]any{"type": "tag", "sha": strings.Repeat("a", 40)}})
			return
		}
		writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": release.Commit}})
	}, "tag"))
	api.HandleFunc("GET "+prefix+"/git/tags/{sha}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.authorized(w, r) {
			return
		}
		for _, release := range g.releases {
			if release.Annotated {
				writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": release.Commit}})
				return
			}
		}
		http.NotFound(w, r)
	})
	api.HandleFunc("GET "+prefix+"/compare/{spec}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.authorized(w, r) {
			return
		}
		commit, ok := strings.CutPrefix(r.PathValue("spec"), "release...")
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, release := range g.releases {
			if release.Commit == commit {
				writeJSON(w, map[string]any{"status": or(release.CompareState, "behind")})
				return
			}
		}
		http.NotFound(w, r)
	})
	api.HandleFunc("GET "+prefix+"/releases/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.authorized(w, r) {
			return
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "want octet-stream", http.StatusUnsupportedMediaType)
			return
		}
		for tag := range g.releases {
			if strconv.FormatInt(assetID(tag), 10) == r.PathValue("id") {
				http.Redirect(w, r, g.Storage.URL+"/blob/"+tag, http.StatusFound)
				return
			}
		}
		http.NotFound(w, r)
	})
	g.API = httptest.NewServer(api)
	t.Cleanup(g.API.Close)
	return g
}

// Put publishes or replaces a release for releaseID.
func (g *GitHub) Put(releaseID string, release Release) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releases["vf-hub-skill-input-"+releaseID] = &release
}

func (g *GitHub) Requests() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.APIRequests
}

func (g *GitHub) authorized(w http.ResponseWriter, r *http.Request) bool {
	g.APIRequests++
	if g.Token != "" && r.Header.Get("Authorization") != "Bearer "+g.Token {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return false
	}
	return true
}

func (g *GitHub) locked(handler func(http.ResponseWriter, *http.Request, string, *Release), key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.authorized(w, r) {
			return
		}
		tag := r.PathValue(key)
		release := g.releases[tag]
		if release == nil {
			http.NotFound(w, r)
			return
		}
		handler(w, r, tag, release)
	}
}

func assetID(tag string) int64 {
	var id int64 = 100
	for _, c := range tag {
		id = (id*31 + int64(c)) % 1000003
	}
	return id
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, value map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, mustJSON(value))
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
