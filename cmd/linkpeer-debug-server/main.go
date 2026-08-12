// linkpeer-debug-server 是联调用 S（扮演 fairpeer 桌面端角色）。
// 直接复用 internal/mobilebridge.Bridge，启动即配对 + 自动确认 + 打印二维码链接，
// 并用一个打印式 CommandExecutor 证明 linkpeer 的加密命令能到达。
//
// 用法：
//
//	linkpeer-debug-server -signal http://<本机局域网IP>:8080
//
// 它不接 fairpeer 的 Controller/前端 —— 那是 M4 桌面端 UI 的事。这里只验证
// linkpeer(Dart, C) ↔ mobilebridge(Go, S) 的 配对/信令/WebRTC/握手/AEAD 链路。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zzycxz/fairpeer/internal/mobilebridge"
)

// debugExec 打印收到的命令 —— 看到 [CMD] 就证明 linkpeer 的 AEAD 加密帧
// 已被 S 解密并路由到 executor（整条加密链路通）。
type debugExec struct{}

func (debugExec) Submit(tab, input, _ string) error {
	fmt.Printf("[CMD] ✓ submit  tab=%s input=%q\n", tab, input)
	return nil
}
func (debugExec) Cancel(tab string) error      { fmt.Printf("[CMD] cancel  tab=%s\n", tab); return nil }
func (debugExec) Steer(tab, text string) error { fmt.Printf("[CMD] steer   tab=%s\n", tab); return nil }
func (debugExec) Pause(string) error           { return nil }
func (debugExec) Resume(string) error          { return nil }
func (debugExec) Approve(string, string, bool, bool, bool) error {
	return nil
}
func (debugExec) Answer(string, string, []string) error      { return nil }
func (debugExec) SetPlan(string, bool) error                 { return nil }
func (debugExec) SetModel(string, string) error              { return nil }
func (debugExec) ListSessions() ([]mobilebridge.SessionInfo, error) { return nil, nil }

func main() {
	signalURL := flag.String("signal", "http://192.168.1.48:8080", "K signal URL (手机可达的局域网地址)")
	flag.Parse()

	store := mobilebridge.NewMemoryKeyStore()
	pub, priv, err := mobilebridge.GenerateLongTerm()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen key: %v\n", err)
		os.Exit(1)
	}

	cfg := mobilebridge.DefaultConfig()
	cfg.SignalURL = *signalURL
	cfg.ReadOnlyDefault = false // 联调：允许 submit，方便验证上行

	audit := mobilebridge.NewAudit("info")
	bridge := mobilebridge.NewBridge(cfg, priv, pub, store, debugExec{}, audit)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.Start(ctx) // 连 K

	time.Sleep(1500 * time.Millisecond) // 等 SignalClient 连上 K

	// auto-confirm：C exchange 后立刻确认（真实桌面端会弹 UI 让用户点）。
	go func() {
		seen := map[string]bool{}
		for {
			for _, p := range bridge.PendingPairings() {
				if !seen[p.PairID] {
					seen[p.PairID] = true
					fmt.Printf("[PAIR] C exchange devC=%s fpC=%s → auto-confirm\n",
						p.DevC, p.FpC)
					if err := bridge.ConfirmPairing(p.PairID); err != nil {
						fmt.Printf("[PAIR] confirm err: %v\n", err)
					} else {
						fmt.Printf("[PAIR] ✓ confirmed，C 现在可连\n")
					}
				}
			}
			time.Sleep(400 * time.Millisecond)
		}
	}()

	code, qrURL, err := bridge.StartPairing()
	if err != nil {
		fmt.Fprintf(os.Stderr, "StartPairing: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n========================================\n")
	fmt.Printf(" linkpeer-debug-server (S)\n")
	fmt.Printf(" devId:  %s\n", mobilebridge.DevID(pub))
	fmt.Printf(" signal: %s\n", *signalURL)
	fmt.Printf(" code:   %s\n", code)
	fmt.Printf(" qrURL（粘到 linkpeer 配对页）:\n %s\n", qrURL)
	fmt.Printf("========================================\n\n")
	fmt.Printf("等 linkpeer 连接…（握手成功后打印 [CMD]，证明加密链路通）\n\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nshutting down")
}
