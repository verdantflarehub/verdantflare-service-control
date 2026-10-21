package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// MarketRepository writes chart sources to the Market repository and starts
// its chart release workflow. Only the GitHub implementation exists today.
type MarketRepository interface {
	CommitChart(ctx context.Context, appID string, files map[string][]byte, message string) (string, error)
	DispatchChartRelease(ctx context.Context, appID string) error
	FindChartRelease(ctx context.Context, commitSHA string) (MarketRun, bool, error)
}

type MarketRun struct {
	ID         int64
	URL        string
	Status     string
	Conclusion string
}

const (
	marketBranch   = "dev"
	marketWorkflow = "publish-charts.yml"
)

type GitHubMarket struct {
	APIURL string
	Repo   string
	Token  string
	Client *http.Client
}

func (g *GitHubMarket) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.APIURL, "/")+"/repos/"+g.Repo+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, domain.NewError(503, "MARKET_REPOSITORY_UNAVAILABLE", "Market 仓库暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, nil
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func marketFailure(step string, status int) error {
	if status >= 500 || status == 0 {
		return domain.NewError(503, "MARKET_REPOSITORY_UNAVAILABLE", "Market 仓库暂时不可用："+step)
	}
	return domain.NewError(502, "MARKET_REPOSITORY_REJECTED", fmt.Sprintf("Market 仓库拒绝了%s（HTTP %d）", step, status))
}

// CommitChart replaces charts/<appId>/ on the dev branch with files in one
// commit; files missing from the archive are deleted.
func (g *GitHubMarket) CommitChart(ctx context.Context, appID string, files map[string][]byte, message string) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if status, err := g.call(ctx, http.MethodGet, "/git/ref/heads/"+marketBranch, nil, &ref); err != nil || status != http.StatusOK {
		return "", firstError(err, marketFailure("读取 dev 分支", status))
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if status, err := g.call(ctx, http.MethodGet, "/git/commits/"+ref.Object.SHA, nil, &commit); err != nil || status != http.StatusOK {
		return "", firstError(err, marketFailure("读取 dev 提交", status))
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if status, err := g.call(ctx, http.MethodGet, "/git/trees/"+commit.Tree.SHA+"?recursive=1", nil, &tree); err != nil || status != http.StatusOK {
		return "", firstError(err, marketFailure("读取仓库目录", status))
	}
	prefix := "charts/" + appID + "/"
	entries := []map[string]any{}
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		var blob struct {
			SHA string `json:"sha"`
		}
		status, err := g.call(ctx, http.MethodPost, "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(files[name]), "encoding": "base64"}, &blob)
		if err != nil || status != http.StatusCreated {
			return "", firstError(err, marketFailure("上传文件", status))
		}
		entries = append(entries, map[string]any{"path": prefix + name, "mode": "100644", "type": "blob", "sha": blob.SHA})
	}
	for _, entry := range tree.Tree {
		if entry.Type == "blob" && strings.HasPrefix(entry.Path, prefix) {
			if _, keep := files[strings.TrimPrefix(entry.Path, prefix)]; !keep {
				entries = append(entries, map[string]any{"path": entry.Path, "mode": "100644", "type": "blob", "sha": nil})
			}
		}
	}
	var newTree, newCommit struct {
		SHA string `json:"sha"`
	}
	if status, err := g.call(ctx, http.MethodPost, "/git/trees", map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &newTree); err != nil || status != http.StatusCreated {
		return "", firstError(err, marketFailure("创建目录树", status))
	}
	if status, err := g.call(ctx, http.MethodPost, "/git/commits", map[string]any{"message": message, "tree": newTree.SHA, "parents": []string{ref.Object.SHA}}, &newCommit); err != nil || status != http.StatusCreated {
		return "", firstError(err, marketFailure("创建提交", status))
	}
	status, err := g.call(ctx, http.MethodPatch, "/git/refs/heads/"+marketBranch, map[string]any{"sha": newCommit.SHA, "force": false}, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnprocessableEntity || status == http.StatusConflict {
		return "", domain.NewError(409, "MARKET_BRANCH_MOVED", "Market dev 分支在提交期间已变化，请重试")
	}
	if status != http.StatusOK {
		return "", marketFailure("更新 dev 分支", status)
	}
	return newCommit.SHA, nil
}

func (g *GitHubMarket) DispatchChartRelease(ctx context.Context, appID string) error {
	status, err := g.call(ctx, http.MethodPost, "/actions/workflows/"+marketWorkflow+"/dispatches",
		map[string]any{"ref": marketBranch, "inputs": map[string]string{"charts": appID}}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return marketFailure("触发 Chart 发行", status)
	}
	return nil
}

func (g *GitHubMarket) FindChartRelease(ctx context.Context, commitSHA string) (MarketRun, bool, error) {
	var runs struct {
		WorkflowRuns []struct {
			ID         int64  `json:"id"`
			HTMLURL    string `json:"html_url"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_runs"`
	}
	query := url.Values{"branch": {marketBranch}, "event": {"workflow_dispatch"}, "head_sha": {commitSHA}, "per_page": {"5"}}
	status, err := g.call(ctx, http.MethodGet, "/actions/workflows/"+marketWorkflow+"/runs?"+query.Encode(), nil, &runs)
	if err != nil || status != http.StatusOK {
		return MarketRun{}, false, firstError(err, marketFailure("查询 Chart 发行", status))
	}
	if len(runs.WorkflowRuns) == 0 {
		return MarketRun{}, false, nil
	}
	run := runs.WorkflowRuns[0]
	return MarketRun{ID: run.ID, URL: run.HTMLURL, Status: run.Status, Conclusion: run.Conclusion}, true, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
