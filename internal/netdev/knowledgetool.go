package netdev

// knowledgetool.go — netdev_knowledge：外置知识表的加载通道（BLUETEAM 批1 /
// SKILL_ORCHESTRATION_SPEC §3.5-D）。知识不住在 body（body 永远薄），也不
// 要求子代理猜状态目录路径——一个原子读工具按 id 取表，内容哈希落审计
// （诊断/核查结论可追溯"当时用的是哪版表"）。用户在 user-knowledge/ 的
// 同 id 覆盖由 knowledge.Load 的优先级保证。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zzycxz/fairpeer/internal/netdev/knowledge"
)

type knowledgeTool struct{}

func (t *knowledgeTool) Name() string { return "netdev_knowledge" }

func (t *knowledgeTool) Description() string {
	return "Load ONE externalized knowledge table for the blue-team entry forms: ids segment-priors (网段先验：网关候选/采样点位/≥2活闸门/TTL 解读/段职能→动作), " +
		"credential-spots (H1 凭据存放点巡检表——只判存在与暴露，不取值), host-risk-checks (H2 本机风险检查——阳性判据+误报回退成对). " +
		"Run it at the start of 入口=主机/网段 workflows and follow the table entries; the content hash lands in the audit chain."
}

func (t *knowledgeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "enum": ["segment-priors", "credential-spots", "host-risk-checks"], "description": "knowledge table id"}
		},
		"required": ["id"]
	}`)
}

func (t *knowledgeTool) ReadOnly() bool { return true }

func (t *knowledgeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	id := strings.TrimSpace(a.ID)
	if id == "" {
		return "", fmt.Errorf("netdev_knowledge: id is required")
	}
	b, err := knowledge.Load(id)
	if err != nil {
		return "", err
	}
	// §3.5-D：内容哈希入审计——结论可追溯"当时用的是哪版表"（用户覆盖
	// 与内置因此可区分）。
	sum := sha256.Sum256(b)
	_ = AppendAudit(Audit{Device: "(knowledge)", Command: fmt.Sprintf("%s@%s", id, hex.EncodeToString(sum[:6])), Class: "read", Status: AuditOK, OutputBytes: len(b)})
	return string(b), nil
}
