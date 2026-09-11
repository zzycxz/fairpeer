package agent

// view_image.go — G6（CODEX_GAP_AUDIT）：view_image 工具结果的图像直读。
// 工具输出 "view_image: <abs path>" 标记行；这里把文件读成 base64 data URL，
// 组成 [文字说明, image_url] 两个 content part 附进 tool 消息，多模态模型
// 直接看到图。任何一步失败都降级为纯文本结果——看图是增强，不是依赖。

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// viewImageMarker must stay in sync with the builtin view_image tool's output.
const viewImageMarker = "view_image: "

// maxViewImageBytes mirrors the tool's cap (3 MiB raw ≈ 4 MB encoded — inside
// Anthropic's 5 MB-per-image wire limit); the agent re-checks because it reads
// the bytes for the data URL.
const maxViewImageBytes = 3 << 20

// viewMediaTypes maps the extensions view_image accepts to the MIME type the
// data URL needs.
var viewMediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif",
}

// toolResultContent converts a tool's string output into the message Content.
// Plain tools return the string unchanged; view_image results become text +
// image content parts so a vision-capable model sees the image.
func toolResultContent(toolName, output string) any {
	if toolName != "view_image" {
		return output
	}
	p := output
	if i := strings.Index(p, "\n"); i >= 0 {
		p = p[:i] // the marker line only
	}
	p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), viewImageMarker))
	if p == "" {
		return output
	}
	ext := strings.ToLower(filepath.Ext(p))
	mt, ok := viewMediaTypes[ext]
	if !ok {
		return output
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 || len(b) > maxViewImageBytes {
		return output // degrade to the text result; never break the turn
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mt, base64.StdEncoding.EncodeToString(b))
	return []provider.ContentPart{
		{Type: "text", Text: output},
		{Type: "image_url", ImageURL: &provider.ImageURL{URL: dataURL}},
	}
}
