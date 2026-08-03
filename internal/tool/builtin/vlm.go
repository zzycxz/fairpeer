package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// VLM 可切换调用层。screen_perceive 的视觉理解走一条降级链：优先 provider
// 多模态模型（qwen3.6-27b 等）。boot.go 根据 [cowork] vlm_backend/vlm_model
// 构造链后通过 SetVLMChain 注入。
//
// 链中每个 backend 独立尝试：成功即返回，失败（5xx/超时/空）则尝试下一个，
// 全部失败时返回最后一个错误。空链表示未配置任何 vision backend，CallVLM
// 直接返回错误，引导用户在 Settings 里配置一个支持 vision 的模型。

// VLMBackendKind identifies one backend in the VLM degradation chain.
type VLMBackendKind int

const (
	// VLMBackendProvider is a provider-layer multimodal chat model (qwen/kimi/etc).
	// The model must be vision-capable; the runner is injected from boot.
	VLMBackendProvider VLMBackendKind = iota
)

// VLMBackend is one link in the VLM degradation chain. Kind selects the backend;
// Model is the provider model ref (Kind=Provider only); Label is the
// human-readable name surfaced in errors and logs.
type VLMBackend struct {
	Kind  VLMBackendKind
	Model string // Kind=Provider: model ref (e.g. "qwen/qwen3.6-27b")
	Label string // display name for logs/errors
}

var (
	vlmChainMu     sync.RWMutex
	globalVLMChain []VLMBackend
)

// SetVLMChain replaces the VLM degradation chain. boot.go calls this after
// resolving config. An empty chain is accepted as-is; CallVLM will then return a
// "no VLM backend configured" error, guiding the user to set a vision-capable
// model in Settings.
func SetVLMChain(chain []VLMBackend) {
	vlmChainMu.Lock()
	defer vlmChainMu.Unlock()
	globalVLMChain = append([]VLMBackend(nil), chain...)
}

// vlmChain returns a snapshot of the current chain under the read lock. Returns
// nil when SetVLMChain was never called or was called with an empty chain.
func vlmChain() []VLMBackend {
	vlmChainMu.RLock()
	defer vlmChainMu.RUnlock()
	return append([]VLMBackend(nil), globalVLMChain...)
}

// CallVLM sends an image (base64 data URL) + prompt to the VLM degradation chain
// and returns the first backend's text response. Each backend is tried in order;
// on failure (error or empty result) the next is attempted, and the last error
// is surfaced only when every backend failed. When the chain is empty (no vision
// backend configured) it returns a clear configuration error. This is the
// unified entry for the screen_perceive loop.
func CallVLM(ctx context.Context, imgDataURL string, prompt string) (string, error) {
	chain := vlmChain()
	if len(chain) == 0 {
		return "", fmt.Errorf("no VLM backend configured: set a vision-capable model in Settings")
	}
	var lastErr error
	for _, b := range chain {
		text, err := callVLMBackend(ctx, b, imgDataURL, prompt)
		if err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", b.Label, err)
		} else {
			lastErr = fmt.Errorf("%s: empty response", b.Label)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no VLM backend configured: set a vision-capable model in Settings")
	}
	return "", lastErr
}

// callVLMBackend dispatches one backend. Kept separate so CallVLM's retry loop
// stays uniform regardless of Kind.
func callVLMBackend(ctx context.Context, b VLMBackend, imgDataURL, prompt string) (string, error) {
	switch b.Kind {
	case VLMBackendProvider:
		return callProviderVLM(ctx, b.Model, imgDataURL, prompt)
	default:
		return "", fmt.Errorf("unknown VLM backend kind %d", b.Kind)
	}
}

// callProviderVLM uses the provider layer's multimodal chat (qwen/kimi/etc). The
// provider already supports image_url content parts (provider.ImageContent); we
// construct a multimodal user message and run it through the injected runner.
// The model must be vision-capable (provider.Vision=true), enforced by boot.
func callProviderVLM(ctx context.Context, model, imgDataURL, prompt string) (string, error) {
	if strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("vlm_model is empty — set [cowork] vlm_model to a vision-capable model (e.g. qwen/qwen3.6-27b)")
	}
	content := provider.ImageContent(prompt, imgDataURL)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: content},
	}
	resp, err := runProviderChat(ctx, model, msgs)
	if err != nil {
		return "", fmt.Errorf("provider VLM (%s): %w", model, err)
	}
	if len(resp) > 0 {
		return provider.ContentString(resp[0].Content), nil
	}
	return "", nil
}

// runProviderChat is a thin bridge to the provider layer. boot.go injects the
// real runner via SetProviderChatRunner; the zero value returns an error so a
// misconfigured boot surfaces clearly instead of a nil panic.
var runProviderChat = func(ctx context.Context, model string, msgs []provider.Message) ([]provider.Message, error) {
	return nil, fmt.Errorf("provider VLM bridge not initialized")
}

// SetProviderChatRunner injects the provider chat runner from boot.go (which has
// access to the resolved provider config). Avoids a circular import between
// tool/builtin and provider/boot.
func SetProviderChatRunner(fn func(ctx context.Context, model string, msgs []provider.Message) ([]provider.Message, error)) {
	runProviderChat = fn
}
