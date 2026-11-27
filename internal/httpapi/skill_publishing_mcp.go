package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

// The skill publishing MCP is a separate MCP server from the Hub market /mcp:
// its own entry, skill_publish tokens only, and only skill release tools.
const maxSkillPublishingMCPBodyBytes = 64 << 10

// Registering fetches up to 32 MiB from GitHub (bounded by
// skillsource.FetchTimeout); give the response more time than the server's
// default write timeout, still under the Hub gateway's 60 s.
const skillPublishingWriteTimeout = 55 * time.Second

func (s *Server) skillPublishingMCP(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(skillPublishingWriteTimeout))
	s.serveMCP(w, r, mcpEndpoint{realm: "skill-publishing-mcp", serverName: "verdantflare-skill-publishing",
		purposes: []string{control.MCPTokenPurposeSkill},
		maxBody:  maxSkillPublishingMCPBodyBytes, maxBodyMsg: "MCP 请求体超过 64 KiB",
		tools: skillPublishingTools, call: s.callSkillPublishingTool, failure: skillPublishingFailure})
}

func skillPublishingFailure(err error, _ string) (*domain.Error, bool) {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure, false
	}
	return domain.NewError(http.StatusServiceUnavailable, "SKILL_PUBLISH_UNAVAILABLE", "技能发行服务暂时不可用，请先用 skill_publish.get 回读再重试"), true
}

func decodeSkillArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.NewError(http.StatusBadRequest, "INVALID_ARGUMENTS", "技能发布工具参数无效或包含不支持字段")
	}
	return nil
}

type skillReleaseArguments struct {
	ReleaseID             string `json:"releaseId"`
	ExpectedArchiveSHA256 string `json:"expectedArchiveSha256"`
	Reason                string `json:"reason"`
}

func (s *Server) callSkillPublishingTool(r *http.Request, identity control.MCPIdentity, name string, raw json.RawMessage) (any, error) {
	ctx := r.Context()
	actor := control.SkillActor{Subject: identity.Subject, TokenID: identity.TokenID, Tool: name}
	var args skillReleaseArguments
	if name != "skill_publish.list" {
		if err := decodeSkillArguments(raw, &args); err != nil {
			return nil, err
		}
	}
	switch name {
	case "skill_publish.list":
		var none struct{}
		if err := decodeSkillArguments(raw, &none); err != nil {
			return nil, err
		}
		return s.service.ListManagedSkillReleases(ctx, identity.Subject)
	case "skill_publish.get":
		return s.service.SkillReleaseDetailFor(ctx, identity.Subject, args.ReleaseID)
	case "skill_publish.readiness":
		return s.service.SkillReleaseReadinessFor(ctx, identity.Subject, args.ReleaseID)
	case "skill_publish.register":
		release, created, err := s.service.RegisterSkillRelease(ctx, actor, args.ReleaseID, args.ExpectedArchiveSHA256)
		if err != nil {
			return nil, err
		}
		return map[string]any{"created": created, "release": release}, nil
	case "skill_publish.publish":
		if _, err := s.service.PublishSkillRelease(ctx, actor, args.ReleaseID, args.ExpectedArchiveSHA256); err != nil {
			return nil, err
		}
		return s.service.SkillReleaseDetailFor(ctx, identity.Subject, args.ReleaseID)
	case "skill_publish.withdraw":
		if _, err := s.service.WithdrawSkillRelease(ctx, actor, args.ReleaseID, args.ExpectedArchiveSHA256, args.Reason); err != nil {
			return nil, err
		}
		return s.service.SkillReleaseDetailFor(ctx, identity.Subject, args.ReleaseID)
	}
	return nil, domain.NewError(http.StatusNotFound, "TOOL_NOT_FOUND", "技能发布 MCP 没有该工具")
}

func skillPublishingTools(string) []hubMCPTool {
	releaseID := map[string]any{"type": "string", "description": "发行号，例如 0.3.0；对应技能仓库标签 vf-hub-skill-input-<releaseId>"}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "发行包 ZIP 的 SHA-256（小写十六进制）"}
	schema := func(required []string, properties map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
	}
	return []hubMCPTool{
		{Name: "skill_publish.list", Description: "列出技能发行（候选／已发布／已撤回）及当前发行，不含包字节。",
			InputSchema: schema([]string{}, map[string]any{})},
		{Name: "skill_publish.get", Description: "回读一个发行：状态、摘要、来源回执、发布检查结果和最近的运营记录。网络结果不明时先调用它，不要换发行号重试。",
			InputSchema: schema([]string{"releaseId"}, map[string]any{"releaseId": releaseID})},
		{Name: "skill_publish.register", Description: "从技能仓库 verdantflare-skills 的发布工作流 Release 拉取发行包，核对来源与摘要后登记为候选。不接受 URL、文件或包内容；同号同包重复调用返回原候选。",
			InputSchema: schema([]string{"releaseId", "expectedArchiveSha256"}, map[string]any{"releaseId": releaseID, "expectedArchiveSha256": digest})},
		{Name: "skill_publish.readiness", Description: "只读发布预检：返回 ready、缺项、来源是否已核对、包内摘要是否一致；不改变当前发行。",
			InputSchema: schema([]string{"releaseId"}, map[string]any{"releaseId": releaseID})},
		{Name: "skill_publish.publish", Description: "发布候选，使其成为成员可下载的当前发行。服务端会再次执行发布检查。",
			InputSchema: schema([]string{"releaseId", "expectedArchiveSha256"}, map[string]any{"releaseId": releaseID, "expectedArchiveSha256": digest})},
		{Name: "skill_publish.withdraw", Description: "撤回已发布的发行：此后该版本下载返回 410，已下载到用户本机的文件不受影响。撤回后不能重新发布。",
			InputSchema: schema([]string{"releaseId", "expectedArchiveSha256", "reason"}, map[string]any{"releaseId": releaseID, "expectedArchiveSha256": digest,
				"reason": map[string]any{"type": "string", "minLength": 2, "maxLength": 200, "description": "撤回原因"}})},
	}
}
