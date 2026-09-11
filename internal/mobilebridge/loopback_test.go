package mobilebridge

import (
	"testing"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"strings"
)

// 测试链路只绑回环：pion 默认在每块网卡上收集 host candidate（绑各接口
// IP），Windows 防火墙对非回环监听会弹"允许访问"授权窗，而 go test 每次编
// 出的临时 exe 路径都不同，等于每跑一次弹一次窗。init 注入 seHook，把
// Bridge 内部创建的 PC 也限制到 127.0.0.1，mDNS 一并关闭。seHook 只在本包
// 测试二进制里非 nil，生产构建不受影响。
func init() {
	seHook = func(se *webrtc.SettingEngine) {
		// pion/webrtc v4: filter 只给接口名（v3 时代给 *net.IP）；mDNS 开关改名
		// SetICEMulticastDNSMode 且枚举在 ice 包。按名保留回环接口。
		se.SetInterfaceFilter(func(name string) (keep bool) {
			n := strings.ToLower(name)
			return strings.Contains(n, "loopback") || n == "lo" || strings.HasPrefix(n, "lo")
		})
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	}
}

// newLoopbackPC 返回只绑 127.0.0.1 的测试用 PeerConnection（理由见 init）。
func newLoopbackPC(t testing.TB, cfg webrtc.Configuration) *webrtc.PeerConnection {
	t.Helper()
	se := webrtc.SettingEngine{}
	seHook(&se)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(cfg)
	if err != nil {
		t.Fatalf("loopback PC: %v", err)
	}
	return pc
}
