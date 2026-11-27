// Package skillsource fetches skill release inputs from the one trusted
// source: GitHub Releases that the skills repository's own release workflow
// created. It proves where the bytes came from; skillrelease.ParseHandoffInput
// still proves what they contain.
package skillsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

const (
	// TagPrefix is prepended to a release id to name its tag and asset.
	TagPrefix = "vf-hub-skill-input-"
	// WorkflowActor is the only account allowed to author the release and upload its asset.
	WorkflowActor = "github-actions[bot]"
	// ReleaseBranch must contain the tagged commit.
	ReleaseBranch = "release"
	// MaxArchiveBytes matches skillrelease's archive limit.
	MaxArchiveBytes = 32 << 20
	// FetchTimeout stays under the Hub gateway's 60 s proxy timeout so the
	// gateway never gives up while Control still commits.
	FetchTimeout   = 40 * time.Second
	maxAPIBodySize = 1 << 20
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Client reads one fixed repository. The token, when set, only needs
// read access to that repository's contents.
type Client struct {
	api        *url.URL
	repository string
	token      string
	http       *http.Client
	now        func() time.Time
}

// New builds a client. apiURL is https://api.github.com in production.
func New(apiURL, repository, token string) (*Client, error) {
	api, err := url.Parse(strings.TrimRight(strings.TrimSpace(apiURL), "/"))
	if err != nil || api.Host == "" || (api.Scheme != "https" && api.Scheme != "http") {
		return nil, fmt.Errorf("invalid skill source API URL")
	}
	if !repositoryPattern.MatchString(repository) {
		return nil, fmt.Errorf("invalid skill source repository %q", repository)
	}
	client := &Client{api: api, repository: repository, token: strings.TrimSpace(token), now: func() time.Time { return time.Now().UTC() }}
	client.http = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		// Asset downloads redirect to a storage host: never send our token there.
		if req.URL.Host != client.api.Host {
			req.Header.Del("Authorization")
		}
		return nil
	}}
	return client, nil
}

// Repository is the owner/name this client is fixed to.
func (c *Client) Repository() string { return c.repository }

func unavailable(detail string) error {
	return domain.NewError(http.StatusBadGateway, "skill_source_unavailable", "技能仓库暂时无法访问："+detail+"，请稍后重试")
}

func untrusted(detail string) error {
	return domain.NewError(http.StatusUnprocessableEntity, "skill_source_untrusted", "来源核对未通过："+detail)
}

type release struct {
	ID      int64  `json:"id"`
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		State    string `json:"state"`
		Uploader struct {
			Login string `json:"login"`
		} `json:"uploader"`
	} `json:"assets"`
}

type gitObject struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

// Fetch downloads the asset of tag vf-hub-skill-input-<releaseID> and checks
// that the workflow made it from a commit on the release branch and that its
// SHA-256 is expectedSHA256. The returned commit still has to equal the
// source lock inside the archive; the caller checks that after parsing.
func (c *Client) Fetch(ctx context.Context, releaseID, expectedSHA256 string) ([]byte, domain.SkillReleaseSource, error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	tag := TagPrefix + releaseID
	var source domain.SkillReleaseSource
	var meta release
	if err := c.getJSON(ctx, "/releases/tags/"+url.PathEscape(tag), &meta); err != nil {
		return nil, source, err
	}
	if meta.TagName != tag || meta.Draft {
		return nil, source, untrusted("发行不是该标签的正式 Release")
	}
	if meta.Author.Login != WorkflowActor {
		return nil, source, untrusted("Release 不是由技能仓库的发布工作流创建")
	}
	if len(meta.Assets) != 1 {
		return nil, source, untrusted("Release 必须只有一个发行包资产")
	}
	asset := meta.Assets[0]
	if asset.Name != tag+".zip" || asset.State != "uploaded" || asset.Uploader.Login != WorkflowActor {
		return nil, source, untrusted("发行包资产不是由发布工作流上传的 " + tag + ".zip")
	}
	if asset.Size <= 0 || asset.Size > MaxArchiveBytes {
		return nil, source, untrusted("发行包大小超出 32 MiB 限制")
	}
	commit, err := c.tagCommit(ctx, tag)
	if err != nil {
		return nil, source, err
	}
	var compare struct {
		Status string `json:"status"`
	}
	if err := c.getJSON(ctx, "/compare/"+ReleaseBranch+"..."+commit, &compare); err != nil {
		return nil, source, err
	}
	if compare.Status != "behind" && compare.Status != "identical" {
		return nil, source, untrusted("标签提交不在 " + ReleaseBranch + " 分支上")
	}
	data, err := c.download(ctx, asset.ID, asset.Size)
	if err != nil {
		return nil, source, err
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != expectedSHA256 {
		return nil, source, domain.NewError(http.StatusConflict, "skill_source_digest_mismatch", "发行包 SHA-256 与预期不符："+got)
	}
	source = domain.SkillReleaseSource{Repository: c.repository, Tag: tag, Commit: commit, GitHubReleaseID: meta.ID,
		AssetID: asset.ID, Uploader: asset.Uploader.Login, Size: int64(len(data)), SHA256: expectedSHA256, FetchedAt: c.now()}
	return data, source, nil
}

// tagCommit resolves lightweight and annotated tags to the tagged commit.
func (c *Client) tagCommit(ctx context.Context, tag string) (string, error) {
	var ref gitObject
	if err := c.getJSON(ctx, "/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return "", err
	}
	if ref.Object.Type == "tag" {
		if !commitPattern.MatchString(ref.Object.SHA) {
			return "", untrusted("标签对象无效")
		}
		var annotated gitObject
		if err := c.getJSON(ctx, "/git/tags/"+ref.Object.SHA, &annotated); err != nil {
			return "", err
		}
		ref = annotated
	}
	if ref.Object.Type != "commit" || !commitPattern.MatchString(ref.Object.SHA) {
		return "", untrusted("标签没有指向提交")
	}
	return ref.Object.SHA, nil
}

func (c *Client) request(ctx context.Context, path, accept string) (*http.Response, error) {
	target := c.api.JoinPath("repos", c.repository).String() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, unavailable("请求无效")
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "verdantflare-control")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, domain.NewError(http.StatusGatewayTimeout, "skill_source_timeout", "读取技能仓库超时，请用 skill_publish.get 回读后重试")
		}
		return nil, unavailable("网络错误")
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusNotFound:
		return nil, domain.NewError(http.StatusNotFound, "skill_source_not_found", "技能仓库中没有该发行或其标签")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, unavailable(fmt.Sprintf("HTTP %d（可能触发限流）", res.StatusCode))
	}
	return nil, unavailable(fmt.Sprintf("HTTP %d", res.StatusCode))
}

func (c *Client) getJSON(ctx context.Context, path string, target any) error {
	res, err := c.request(ctx, path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, maxAPIBodySize)).Decode(target); err != nil {
		return unavailable("响应无法解析")
	}
	return nil
}

func (c *Client) download(ctx context.Context, assetID, size int64) ([]byte, error) {
	res, err := c.request(ctx, fmt.Sprintf("/releases/assets/%d", assetID), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxArchiveBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, domain.NewError(http.StatusGatewayTimeout, "skill_source_timeout", "下载发行包超时，请用 skill_publish.get 回读后重试")
		}
		return nil, unavailable("下载中断")
	}
	if int64(len(data)) != size {
		return nil, untrusted("下载长度与资产声明不一致")
	}
	return data, nil
}
