package main

// screenshot_solve.go contains the cross-platform screenshot solving logic:
// capture → VLM solve (with optional web search) → IM push + toast.
// This file has no build tag — it compiles on all platforms.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zzycxz/fairpeer/internal/agent"
	"github.com/zzycxz/fairpeer/internal/boot"
	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/event"
	"github.com/zzycxz/fairpeer/internal/netclient"
	"github.com/zzycxz/fairpeer/internal/provider"
	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

const defaultSolvePrompt = "请从屏幕截图中找到用户当前遇到的问题或题目，然后逐步推理并给出答案。完成后自行验证答案是否正确，如不确定请联网搜索核实。最终给出：1）识别到的题目 2）答案 3）解题过程 4）验证结果。"
const defaultSolveSystemPrompt = "你是一个能看图的解题助手。首先从截图中准确识别出用户正在处理的题目或问题，然后逐步推理。遇到不确定的信息，主动使用联网搜索核实。给出答案后，用逆向推理或代入法验证答案的正确性。如果发现错误，自行纠正后再给出最终答案。"

const (
	// screenshotDebounce is the silent window after the last hotkey press
	// before the queued burst is dispatched to the VLM.
	screenshotDebounce = 3 * time.Second
	// maxScreenshotBurst caps how many screenshots one silent window may
	// accumulate. Reaching the cap dispatches immediately and lets the next
	// press open a fresh burst, bounding stitch memory and payload size.
	maxScreenshotBurst = 10
	// maxStitchDim caps the stitched image's width and height in pixels:
	// several VLM providers (e.g. Anthropic) reject images larger than
	// ~8000px per side. Anything above is uniformly downscaled.
	maxStitchDim = 8000
)

var (
	screenshotMu    sync.Mutex
	screenshotQueue []string
	debounceCancel  context.CancelFunc
)

// boxKernel is an area-averaging filter — the correct kernel for pure
// downscales (CatmullRom and friends alias on large minification factors).
var boxKernel = xdraw.Kernel{
	Support: 1,
	At:      func(t float64) float64 { return 1 },
}

func (a *App) triggerScreenshotSolve() {
	// Hold screenshotMu across capture→encode→enqueue so concurrent triggers
	// (hotkey polling goroutine vs tray menu goroutine) enqueue in press
	// order, and the debounce goroutine never splits a burst mid-press.
	screenshotMu.Lock()
	img, err := builtin.CaptureFullScreen()
	if err != nil || img == nil {
		screenshotMu.Unlock()
		a.emitScreenshotNotice("截图失败: "+fmt.Sprint(err), "")
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		screenshotMu.Unlock()
		a.emitScreenshotNotice("截图编码失败", "")
		return
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	// Burst cap reached: dispatch what we have; this press opens a new burst.
	var flush []string
	if len(screenshotQueue) >= maxScreenshotBurst {
		flush = screenshotQueue
		screenshotQueue = nil
		if debounceCancel != nil {
			debounceCancel()
			debounceCancel = nil
		}
	}

	screenshotQueue = append(screenshotQueue, dataURL)
	count := len(screenshotQueue)

	if debounceCancel != nil {
		debounceCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	debounceCancel = cancel
	screenshotMu.Unlock()

	if flush != nil {
		a.emitScreenshotNotice(fmt.Sprintf("连拍已达 %d 张上限，先发送这 %d 张，后续截图重新计数", len(flush), len(flush)), "")
		go a.doScreenshotSolve(flush)
	}

	if count == 1 {
		a.emitScreenshotNotice("已捕获第 1 张截图（若无后续截图，3秒后自动发送）", "")
	} else {
		a.emitScreenshotNotice(fmt.Sprintf("已捕获第 %d 张截图（3秒后自动发送）", count), "")
	}

	go func() {
		timer := time.NewTimer(screenshotDebounce)
		defer timer.Stop()
		select {
		case <-timer.C:
			screenshotMu.Lock()
			// A newer press cancels ctx before installing its own, so under
			// the lock a live ctx means this goroutine still owns the queue.
			// (CancelFunc values cannot be compared with ==, only ctx can.)
			if ctx.Err() == nil {
				images := screenshotQueue
				screenshotQueue = nil
				debounceCancel = nil
				cancel()
				screenshotMu.Unlock()
				if len(images) > 0 {
					a.doScreenshotSolve(images)
				}
			} else {
				screenshotMu.Unlock()
			}
		case <-ctx.Done():
			// Superseded by a newer press — exit silently.
		}
	}()
}

func (a *App) doScreenshotSolve(images []string) {
	if len(images) > 1 {
		a.emitScreenshotNotice(fmt.Sprintf("正在处理 %d 张截图（自动拼接中），请稍候…", len(images)), "")
		stitched, err := stitchImagesVertically(images)
		if err == nil {
			images = []string{stitched}
		} else {
			slog.Warn("screenshot: vertical stitch failed, falling back to multi-image part", "err", err)
			a.emitScreenshotNotice("自动拼接失败，改为多图发送…", "")
		}
	} else {
		a.emitScreenshotNotice("正在解题中（可能联网搜索，请稍候）…", "")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		cfg, err := config.Load()
		if err != nil {
			a.emitScreenshotNotice("配置读取失败", "")
			return
		}
		model := cfg.Cowork.ScreenshotVLMModel
		if model == "" {
			a.emitScreenshotNotice("未配置图片识别模型：请在设置中选择一个支持视觉的模型", "")
			return
		}
		entry, err := resolveModelEntry(model)
		if err != nil {
			a.emitScreenshotNotice("模型未找到: "+err.Error(), "")
			return
		}
		// Rescue lost Vision attribute due to previous SaveProvider bugs
		if tmpl := globalRegistry.Find(entry.Name); tmpl != nil {
			entry.Vision = tmpl.Vision
		}
		prov, err := boot.NewProviderWithProxy(entry, netclient.ProxySpec{Mode: netclient.ModeAuto}, false)
		if err != nil {
			a.emitScreenshotNotice("模型初始化失败: "+err.Error(), "")
			return
		}
		prompt := cfg.Cowork.ScreenshotPrompt
		if strings.TrimSpace(prompt) == "" {
			prompt = defaultSolvePrompt
		}
		result, err := solveScreenshot(ctx, prov, entry, prompt, images)
		if err != nil {
			a.emitScreenshotNotice("解题失败: "+err.Error(), "")
			return
		}
		a.emitScreenshotNotice(result, "")
		if gw := a.botGW.Load(); gw != nil {
			if dest := a.screenshotPushDest(); dest != "" {
				pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := gw.Push(pushCtx, dest, "🧮 截图解题结果：\n\n"+result); err != nil {
					slog.Warn("screenshot: IM push failed", "dest", dest, "err", err)
				}
				pushCancel()
			}
		}
	}()
}

func solveScreenshot(ctx context.Context, prov provider.Provider, entry *config.ProviderEntry, prompt string, images []string) (string, error) {
	content := provider.ImageContent(prompt, images...)
	if !webSearchKeyConfigured() {
		return solveOneShot(ctx, prov, content)
	}
	reg := (&desktopExpertRunner{}).webSearchRegistry()
	if reg == nil {
		return solveOneShot(ctx, prov, content)
	}
	sess := agent.NewSession(defaultSolveSystemPrompt)
	opts := agent.Options{MaxSteps: 8, ContextWindow: entry.ContextWindow}
	sink := event.FuncSink(func(e event.Event) { _ = e })
	sub := agent.New(prov, reg, sess, opts, sink)
	runErr := sub.Run(ctx, content)
	answer := lastAssistantText(sess)
	if runErr != nil {
		if isMaxStepsPaused(runErr) && answer != "" {
			return answer, nil
		}
		return solveOneShot(ctx, prov, content)
	}
	if answer == "" {
		return solveOneShot(ctx, prov, content)
	}
	return answer, nil
}

func solveOneShot(ctx context.Context, prov provider.Provider, content any) (string, error) {
	req := provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: content}}}
	ch, err := prov.Stream(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for chunk := range ch {
		if chunk.Type == provider.ChunkText {
			b.WriteString(chunk.Text)
		}
		if chunk.Err != nil {
			return b.String(), chunk.Err
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// stitchImagesVertically concatenates the given data-URL images top-to-bottom
// on a white canvas. If the result would exceed maxStitchDim on either side,
// every image is uniformly downscaled (area-averaging) first. Any input that
// fails to decode aborts the whole stitch so the caller falls back to sending
// the originals — images must never be dropped silently.
func stitchImagesVertically(base64Images []string) (string, error) {
	if len(base64Images) == 0 {
		return "", fmt.Errorf("no images")
	}
	if len(base64Images) == 1 {
		return base64Images[0], nil
	}

	var imgs []image.Image
	var maxWidth, totalHeight int
	for i, b64 := range base64Images {
		if idx := strings.Index(b64, ","); idx != -1 {
			b64 = b64[idx+1:]
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("screenshot %d of %d: base64 decode: %w", i+1, len(base64Images), err)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("screenshot %d of %d: image decode: %w", i+1, len(base64Images), err)
		}
		imgs = append(imgs, img)
		bounds := img.Bounds()
		if bounds.Dx() > maxWidth {
			maxWidth = bounds.Dx()
		}
		totalHeight += bounds.Dy()
	}

	scale := 1.0
	if maxWidth > maxStitchDim || totalHeight > maxStitchDim {
		scale = math.Min(float64(maxStitchDim)/float64(maxWidth), float64(maxStitchDim)/float64(totalHeight))
	}

	// Precompute scaled placement rectangles so the per-image heights sum to
	// the final canvas height exactly.
	type placement struct {
		src  image.Image
		rect image.Rectangle
	}
	placements := make([]placement, 0, len(imgs))
	outWidth, outHeight := 0, 0
	for _, img := range imgs {
		b := img.Bounds()
		w := max(1, int(math.Round(float64(b.Dx())*scale)))
		h := max(1, int(math.Round(float64(b.Dy())*scale)))
		placements = append(placements, placement{src: img, rect: image.Rect(0, outHeight, w, outHeight+h)})
		if w > outWidth {
			outWidth = w
		}
		outHeight += h
	}

	finalImg := image.NewRGBA(image.Rect(0, 0, outWidth, outHeight))
	// White backdrop: shots of differing widths would otherwise leave
	// transparent padding that providers re-encoding to JPEG flatten to black.
	xdraw.Draw(finalImg, finalImg.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	for _, p := range placements {
		if scale == 1.0 {
			xdraw.Draw(finalImg, p.rect, p.src, p.src.Bounds().Min, xdraw.Src)
		} else {
			boxKernel.Scale(finalImg, p.rect, p.src, p.src.Bounds(), xdraw.Src, nil)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, finalImg); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func webSearchKeyConfigured() bool {
	return os.Getenv("BRAVE_API_KEY") != "" || os.Getenv("BRAVE_SEARCH_API_KEY") != "" || os.Getenv("EXA_API_KEY") != "" || os.Getenv("LINKUP_API_KEY") != "" || os.Getenv("ANYSEARCH_API_KEY") != ""
}

func resolveModelEntry(modelRef string) (*config.ProviderEntry, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	entry, ok := cfg.ResolveModel(modelRef)
	if !ok {
		return nil, fmt.Errorf("VLM model %q not found in config", modelRef)
	}
	return entry, nil
}

func (a *App) emitScreenshotNotice(message, detail string) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "screenshot:notice", map[string]string{"message": message, "detail": detail})
}

func parseHotkey(s string) (mod, vk int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty hotkey")
	}
	parts := strings.Split(s, "+")
	vk = 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch strings.ToLower(p) {
		case "ctrl", "control":
			mod |= 0x0002
		case "shift":
			mod |= 0x0004
		case "alt":
			mod |= 0x0001
		case "win", "super", "meta", "cmd", "command":
			// "cmd"/"command": macOS keyboards label the modifier ⌘; the bit
			// matches Win-key semantics (chordHeldMacOS maps 0x0008 → "command
			// down"), and unix estop defaults like Ctrl+Alt+Cmd+G rely on it.
			mod |= 0x0008
		default:
			if vk != 0 {
				return 0, 0, fmt.Errorf("multiple keys in hotkey %q", s)
			}
			vk = keyToVK(p)
			if vk == 0 {
				return 0, 0, fmt.Errorf("unknown key %q in hotkey", p)
			}
		}
	}
	if vk == 0 {
		return 0, 0, fmt.Errorf("no key in hotkey %q", s)
	}
	return mod, vk, nil
}

func keyToVK(key string) int {
	if len(key) == 1 {
		c := key[0]
		if c >= 'A' && c <= 'Z' {
			return int(c)
		}
		if c >= 'a' && c <= 'z' {
			return int(c - 32)
		}
		if c >= '0' && c <= '9' {
			return int(c)
		}
	}
	switch strings.ToUpper(key) {
	case "F1":
		return 0x70
	case "F2":
		return 0x71
	case "F3":
		return 0x72
	case "F4":
		return 0x73
	case "F5":
		return 0x74
	case "F6":
		return 0x75
	case "F7":
		return 0x76
	case "F8":
		return 0x77
	case "F9":
		return 0x78
	case "F10":
		return 0x79
	case "F11":
		return 0x7A
	case "F12":
		return 0x7B
	case "SPACE":
		return 0x20
	case "ENTER":
		return 0x0D
	case "TAB":
		return 0x09
	case "PAUSE", "BREAK":
		// The estop default's main key — present on the shared parser so the
		// unix estop can parse the same default combo Windows uses.
		return 0x13
	}
	return 0
}
