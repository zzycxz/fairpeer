package mobilebridge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pairCodeAlphabet omits confusable 0/O/1/I/L (31 chars; NOT a base32 encoding,
// just mod-31 selection for a human-readable 6-char code). Matches
// linkpeersignal.PairStore.GenCode — keep in sync via the pair-flow integration test.
const pairCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func genPairCode() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairCodeAlphabet[int(b[i])%len(pairCodeAlphabet)]
	}
	return string(b)
}

// ErrNoPending means no C is waiting at the given pairId when Confirm is called.
var ErrNoPending = errors.New("no pending pair")

// PendingPair is a C that has exchanged and is awaiting the desktop user's confirm.
type PendingPair struct {
	PairID    string
	DevC      string
	PubC      ed25519.PublicKey
	FpC       string
	CreatedAt time.Time
}

// Pairing drives the desktop side of pair/confirm/unpair against K and the
// local KeyStore. The Wails UI calls StartPairing/Confirm/Unpair; the
// SignalClient feeds OnExchange when K pushes a pair_exchange notice.
type Pairing struct {
	signalURL  string
	devID      string
	longPub    ed25519.PublicKey
	store      KeyStore
	httpc      *http.Client
	audit      *Audit
	onExchange func(pairID, devC, fpC string) // optional UI hook

	// pairAddr 钉死二维码 relay 用哪块网卡的 IP（用户在设置面板手选）。
	// 空 = 自动（默认路由优先 + 全部真实网卡多候选）。
	pairAddr string

	// cloudURL 云跳板 K（跨网候选信令）。非空时：二维码 relay 追加它作
	// 末位候选（手机同网自动选 LAN，跨网回退到云），StartPairing 把同一
	// pairId+code 注册到两台 K（QR 的单个 pid 经任一 K 都能 exchange）。
	// turnParam 进二维码 turn= 字段（"user:pass@host:port"），空 = 不带。
	cloudURL  string
	turnParam string

	mu      sync.Mutex
	pending map[string]*PendingPair
	origin  map[string]string // pairID → exchange 发生的 K（Confirm 回源 POST）

	autoConfirm bool // 联调：OnExchange 后立即 Confirm（不等用户点允许）
}

func NewPairing(signalURL string, pub ed25519.PublicKey, store KeyStore, audit *Audit) *Pairing {
	return &Pairing{
		signalURL: signalURL,
		devID:     DevID(pub),
		longPub:   pub,
		store:     store,
		httpc:     &http.Client{Timeout: 15 * time.Second},
		audit:     audit,
		pending:   map[string]*PendingPair{},
		origin:    map[string]string{},
	}
}

// SetOnExchange installs the UI callback fired when a C scans + exchanges.
func (p *Pairing) SetOnExchange(fn func(pairID, devC, fpC string)) { p.onExchange = fn }

// SetAutoConfirm enables automatic confirmation: OnExchange immediately POSTs
// /pair/confirm + persists C's key, so C's ClientHello (which follows fast)
// passes PeerPub. Desktop user still sees the pending→confirmed UI.
func (p *Pairing) SetAutoConfirm(v bool) { p.autoConfirm = v }

// SetPairAddress 钉死配对二维码使用的网卡 IP（"" 恢复自动）。设置面板调。
func (p *Pairing) SetPairAddress(ip string) { p.pairAddr = ip }

// SetCloudRelay 配置云跳板 K + 二维码 TURN 凭据参数（Bridge.SetCloudRelay
// 热切换时同步到这里）。url 空 = 关闭。
func (p *Pairing) SetCloudRelay(url, turnParam string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cloudURL = url
	p.turnParam = turnParam
}

// NicInfo 是设置面板「配对网卡」下拉框的一条候选。
type NicInfo struct {
	IP        string `json:"ip"`
	Name      string `json:"name"`      // 系统接口名（如 "以太网" / "WLAN"）
	Label     string `json:"label"`     // 友好标签：有线 / Wi-Fi / 局域网
	IsDefault bool   `json:"isDefault"` // 是否为默认（见 defaultLanIPInfo 评判标准）
	Reason    string `json:"reason"`    // 默认判定理由（仅默认项有值，UI 展示用）
}

// ListPairNics 枚举可进二维码的真实网卡候选。
// 评判标准（谁当默认）见 defaultLanIPInfo：默认路由出口（metric 最低、
// 私网、非 TUN）优先；其余真实网卡作多候选补充，.1 网关位垫底。
// 默认项带判定理由（Reason），UI 直接展示"为什么是它"。
func ListPairNics() []NicInfo {
	defIP, defReason := defaultLanIPInfo()
	var out []NicInfo
	seen := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var tail []NicInfo
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			isVirtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() || ip4.IsLoopback() ||
				ip4.IsLinkLocalUnicast() {
				continue
			}
			if seen[ip4.String()] {
				continue
			}
			seen[ip4.String()] = true
			nic := NicInfo{
				IP: ip4.String(), Name: ifc.Name, Label: nicLabel(ifc.Name),
				IsDefault: ip4.String() == defIP,
				Reason:    map[bool]string{true: defReason, false: ""}[ip4.String() == defIP],
			}
			if ip4[3] == 1 {
				tail = append(tail, nic) // 网关位地址（虚拟网卡常见）垫底
			} else {
				out = append(out, nic)
			}
		}
	}
	return append(out, tail...)
}

// nicLabel 从接口名推断友好标签（中英文系统接口名）。
func nicLabel(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "wlan"), strings.Contains(n, "wi-fi"),
		strings.Contains(n, "wifi"), strings.Contains(n, "wireless"):
		return "Wi-Fi"
	case strings.Contains(n, "以太网"), strings.Contains(n, "ethernet"),
		strings.Contains(n, "eth"):
		return "有线"
	default:
		return "局域网"
	}
}

// httpURL 把 WS scheme 的信令地址转成 HTTP（wss→https、ws→http）。
// signal_url 沿用 SignalClient 的 WS 地址形态，但 /pair/register、
// /pair/confirm 是普通 HTTP POST——Go 的 http.Client 不认 ws/wss scheme，
// 默认配置（wss://signal.linkpeer.app）下会直接报
// "unsupported protocol scheme \"wss\""。
func httpURL(signalURL string) string {
	switch {
	case strings.HasPrefix(signalURL, "wss://"):
		return "https://" + strings.TrimPrefix(signalURL, "wss://")
	case strings.HasPrefix(signalURL, "ws://"):
		return "http://" + strings.TrimPrefix(signalURL, "ws://")
	}
	return signalURL
}

// relayCandidates 计算二维码 relay 字段：用户钉死的网卡优先（单候选），
// 否则自动多候选（默认路由出口优先 + 其余真实网卡）；云跳板开启时把云 K
// 追加为末位候选（手机同网自动选 LAN，跨网回退到云）。
func (p *Pairing) relayCandidates() []string {
	var out []string
	if p.pairAddr != "" {
		out = lanRelayURLsFor(p.signalURL, p.pairAddr)
	} else {
		out = lanRelayURLs(p.signalURL)
	}
	p.mu.Lock()
	cloud := p.cloudURL
	p.mu.Unlock()
	if cloud != "" {
		for _, r := range out { // 主信令已是该云 K（单 K 模式）时不重复
			if r == cloud {
				return out
			}
		}
		out = append(out, cloud)
	}
	return out
}

// lanRelayURLsFor 构造「指定 IP」的单候选（钉死模式）。非 loopback 的
// signal_url（公网 K）仍原样透传。
func lanRelayURLsFor(signalURL, ip string) []string {
	u, err := url.Parse(signalURL)
	if err != nil {
		return []string{signalURL}
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return []string{signalURL}
	}
	host := ip
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(ip, port)
	}
	u.Host = host
	return []string{u.String()}
}

// lanRelayURLs 返回二维码 relay 的候选地址列表（逗号拼接进 linkpeer://pair URL，
// 手机端逐个尝试或让用户手选）。
// signal_url 为 loopback（K 与 S 同机联调）时：列出本机所有真实网卡的 LAN
// 地址——默认路由出口优先，其余网卡补充（有线+WiFi 同时在线、跨网段环境
// 下总有一个与手机可达）；非 loopback（公网 K）返回单元素原样，生产行为不变。
func lanRelayURLs(signalURL string) []string {
	u, err := url.Parse(signalURL)
	if err != nil {
		return []string{signalURL}
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return []string{signalURL}
	}
	mk := func(ip string) string {
		if port := u.Port(); port != "" {
			return u.Scheme + "://" + net.JoinHostPort(ip, port)
		}
		return u.Scheme + "://" + ip
	}
	var out []string
	seen := map[string]bool{}
	add := func(ip string) {
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		out = append(out, mk(ip))
	}
	add(defaultLanIP()) // 默认路由出口优先（已过滤 Clash fake-ip）
	for _, ip := range lanIPs() {
		add(ip)
	}
	if len(out) == 0 {
		return []string{signalURL}
	}
	return out
}

// defaultLanIP 取默认网卡 IP（评判标准的实现入口）。
func defaultLanIP() string {
	ip, _ := defaultLanIPInfo()
	return ip
}

// defaultLanIPInfo 返回 (默认网卡 IP, 判定理由)。评判标准按优先级：
//
//  1. 系统路由表的默认路由出口（Windows 解析 `route print -4`；跨平台
//     走 UDP 路由决策）——流量真实出口，手机可达性最强。取 metric 最低
//     且接口 IP 为私网的一条：Clash TUN 的 fake-ip（198.18.x，非私网）
//     和 0.0.0.0/1 劫持路由（掩码非 0.0.0.0）都被自然排除。
//  2. 物理网卡枚举回退：接口名匹配 以太网/Ethernet/WLAN/Wi-Fi 的真实
//     网卡，.1 网关位（虚拟网卡常见）垫底。
//
// 完全无默认路由（离线）时返回 ("", "")。
func defaultLanIPInfo() (string, string) {
	if runtime.GOOS == "windows" {
		if es := windowsDefaultRoutes(); len(es) > 0 {
			return es[0].ifaceIP, fmt.Sprintf("默认路由出口 · metric %d · %s", es[0].metric, es[0].ifName)
		}
	}
	// 跨平台兜底：UDP「连接」不发包只做路由决策，读出口 IP（须私网，
	// 否则是被代理 TUN 劫持的 fake-ip，拒收）。
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if ip4 := addr.IP.To4(); ip4 != nil && ip4.IsPrivate() {
				conn.Close()
				return ip4.String(), "默认路由出口（UDP 探测）"
			}
		}
		conn.Close()
	}
	if ips := lanIPs(); len(ips) > 0 {
		return ips[0], "物理网卡（枚举回退）"
	}
	return "", ""
}

// routeEntry 是路由表里的一条默认路由（0.0.0.0/0）。
type routeEntry struct {
	ifaceIP string
	metric  int
	ifName  string
}

// windowsDefaultRoutes 解析 `route print -4`：返回 0.0.0.0/0 条目，
// metric 升序、去重。中英文表头兼容（按字段位置解析，不依赖表头文案）。
// 800ms 超时——解析失败只是降级到 UDP 探测，不阻塞配对。
func windowsDefaultRoutes() []routeEntry {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "route", "print", "-4").Output()
	if err != nil {
		return nil
	}
	return parseRoutePrint(string(out))
}

// parseRoutePrint 从 route print 输出中提取默认路由条目。
// 行格式（中英文系统字段位置一致）：
//
//	0.0.0.0    0.0.0.0    <网关>    <本机接口IP>    <metric>
//
// 活动路由 + 持久路由两个段落会重复出现同一条目，按 (IP,metric) 去重。
func parseRoutePrint(out string) []routeEntry {
	var entries []routeEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue // 非 0.0.0.0/0（Clash 的 /1 劫持路由掩码是 128.0.0.0，被排除）
		}
		ip := net.ParseIP(f[3])
		if ip == nil || ip.To4() == nil || !ip.To4().IsPrivate() {
			continue // TUN fake-ip（198.18.x 非私网）排除
		}
		metric, err := strconv.Atoi(f[4])
		if err != nil {
			continue
		}
		key := f[3] + "/" + f[4]
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, routeEntry{ifaceIP: f[3], metric: metric, ifName: nicNameOfIP(f[3])})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].metric < entries[j].metric })
	return entries
}

// nicNameOfIP 反查 IP 属于哪块网卡（找不到返回空）。
func nicNameOfIP(ipStr string) string {
	target := net.ParseIP(ipStr)
	if target == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(target) {
				return ifc.Name
			}
		}
	}
	return ""
}

// lanIPs 枚举所有真实（非虚拟）网卡的私网 IPv4：isVirtualIface 排除
// Meta/VMware/WSL/Hyper-V 等，跳过 169.254；.1 结尾的网关位地址（虚拟网卡
// 常见）降级到列表末尾。
func lanIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out, gatewayTail []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			isVirtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() || ip4.IsLoopback() ||
				ip4.IsLinkLocalUnicast() {
				continue
			}
			if ip4[3] == 1 {
				gatewayTail = append(gatewayTail, ip4.String())
			} else {
				out = append(out, ip4.String())
			}
		}
	}
	return append(out, gatewayTail...)
}

// virtualIfaceKeywords 匹配接口名（小写比较）。Windows 的接口友好名如
// "以太网"/"WLAN" 不命中；"Meta"（Clash TUN）、"VMware Network Adapter
// VMnet1"、"vEthernet (Hyper-V)"、"TAP-Windows" 等命中。
var virtualIfaceKeywords = []string{
	"meta", "clash", "mihomo", "singbox", "sing-box", "tun", "tap",
	"vmware", "vmnet", "virtualbox", "wsl", "hyper-v", "vethernet",
	"loopback", "zerotier", "tailscale", "wireguard", "openvpn", "vpn",
}

func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, kw := range virtualIfaceKeywords {
		if strings.Contains(n, kw) {
			return true
		}
	}
	return false
}

// StartPairing generates a code, registers with K(s), returns the QR payload
// (linkpeer://pair?...). The QR carries the fingerprint out-of-band so C can
// defeat a MITM'd K at the exchange step.
//
// 云跳板开启时双 K 注册：S 自己生成 pairId，把同一 pairId+code 注册到主 K
// 与云 K —— QR 里的单个 pid 经任一 K 都能 exchange。云 K 注册失败（VPS
// 挂了/断网）不阻塞局域网配对：候选里去掉云地址即可。
func (p *Pairing) StartPairing() (code, qrURL string, err error) {
	code = genPairCode()
	pubSB64 := b64(p.longPub)
	fp := Fingerprint(p.longPub)
	pid := make([]byte, 16)
	if _, err := rand.Read(pid); err != nil {
		return "", "", err
	}
	pairID := b32.EncodeToString(pid)
	body, _ := json.Marshal(map[string]string{
		"pairId": pairID,
		"code":   code, "devS": p.devID, "pubS": pubSB64, "fpS": fp,
	})
	resp, err := p.httpc.Post(httpURL(p.signalURL)+"/pair/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		eb, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("register: %d %s", resp.StatusCode, eb)
	}
	var r struct {
		PairID    string `json:"pairId"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", err
	}
	if r.PairID == "" {
		return "", "", errors.New("register: empty pairId")
	}
	// 旧版主 K 忽略请求里的 pairId 自行生成——云注册跟随实际生效的 pid，
	// 保证 QR 的单个 pid 经两台 K 都有效。
	if r.PairID != pairID {
		pairID = r.PairID
		body, _ = json.Marshal(map[string]string{
			"pairId": pairID,
			"code":   code, "devS": p.devID, "pubS": pubSB64, "fpS": fp,
		})
	}
	// 云 K 同码注册（失败降级：二维码不带云候选，局域网配对不受影响）
	p.mu.Lock()
	cloud, turnParam := p.cloudURL, p.turnParam
	p.mu.Unlock()
	if cloud != "" && cloud != p.signalURL {
		cResp, cErr := p.httpc.Post(httpURL(cloud)+"/pair/register", "application/json", bytes.NewReader(body))
		if cErr != nil {
			p.audit.Info("cloud_register_failed", p.devID, "err", cErr.Error())
			cloud = ""
		} else {
			io.Copy(io.Discard, cResp.Body)
			cResp.Body.Close()
			if cResp.StatusCode != 200 {
				p.audit.Info("cloud_register_rejected", p.devID, "status", cResp.StatusCode)
				cloud = ""
			}
		}
	}
	relays := p.relayCandidates()
	if cloud == "" { // 云注册失败：从候选里剔除云地址（手机不用白等超时）
		kept := relays[:0]
		p.mu.Lock()
		cloudCfg := p.cloudURL
		p.mu.Unlock()
		for _, r := range relays {
			if r != cloudCfg {
				kept = append(kept, r)
			}
		}
		relays = kept
	}
	qrURL = fmt.Sprintf("linkpeer://pair?pid=%s&code=%s&fp=%s&dev=%s&relay=%s",
		r.PairID, code, fp, p.devID, strings.Join(relays, ","))
	if turnParam != "" && cloud != "" { // TURN 只在云路径存在时有意义
		qrURL += "&turn=" + url.QueryEscape(turnParam)
	}
	p.audit.PairStart(p.devID)
	return code, qrURL, nil
}

// OnExchange handles a pair_exchange notice pushed from K (delivered by the
// SignalClient). Records C as pending and fires the UI hook.
func (p *Pairing) OnExchange(msg SignalMsg) {
	p.OnExchangeFrom(msg, p.signalURL)
}

// OnExchangeFrom 带 exchange 发生源 K 的通知入口（Bridge 双链路分别传入）。
// Confirm 必须回源 POST——pair 记录（含 PubC）存在 exchange 发生的那台 K 上。
func (p *Pairing) OnExchangeFrom(msg SignalMsg, kURL string) {
	pubC, err := b64d(msg.PubC)
	if err != nil || len(pubC) != ed25519.PublicKeySize {
		p.audit.Error("pair_exchange_bad_pubc", msg.DevC, err)
		return
	}
	pp := &PendingPair{
		PairID: msg.PairID, DevC: msg.DevC,
		PubC: ed25519.PublicKey(pubC), FpC: msg.FpC,
		CreatedAt: time.Now(),
	}
	p.mu.Lock()
	p.pending[msg.PairID] = pp
	if kURL != "" {
		p.origin[msg.PairID] = kURL
	}
	auto := p.autoConfirm
	p.mu.Unlock()
	if p.onExchange != nil {
		p.onExchange(msg.PairID, msg.DevC, msg.FpC)
	}
	// 联调捷径：自动确认。C 扫码 exchange 后会在几百 ms 内连 WebRTC 发
	// ClientHello；手动确认来不及。异步 Confirm（POST + store.Set）通常比
	// ICE 协商快，所以 ClientHello 到达时 pubC 已落盘。
	if auto {
		go func() { _ = p.Confirm(msg.PairID) }()
	}
}

// Pending lists C's awaiting confirm (for the Wails UI).
func (p *Pairing) Pending() []PendingPair {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PendingPair, 0, len(p.pending))
	for _, pp := range p.pending {
		out = append(out, *pp)
	}
	return out
}

// Confirm: the desktop user accepted. POST /pair/confirm to K, then persist
// C's public key. After this, C can handshake (Pairing.PeerPub returns its key).
func (p *Pairing) Confirm(pairID string) error {
	p.mu.Lock()
	pp := p.pending[pairID]
	kURL := p.origin[pairID]
	p.mu.Unlock()
	if pp == nil {
		return ErrNoPending
	}
	if kURL == "" {
		kURL = p.signalURL // 未知来源（老流程/测试）：回主 K
	}
	body, _ := json.Marshal(map[string]string{"pairId": pairID, "devS": p.devID})
	resp, err := p.httpc.Post(httpURL(kURL)+"/pair/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("confirm: %d", resp.StatusCode)
	}
	if err := p.store.Set("mobilebridge.peer."+pp.DevC+".pub", []byte(pp.PubC)); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.pending, pairID)
	delete(p.origin, pairID)
	p.mu.Unlock()
	p.audit.PairConfirmed(pp.DevC, p.devID)
	return nil
}

// Reject drops a pending pair without persisting (desktop user declined).
func (p *Pairing) Reject(pairID string) {
	p.mu.Lock()
	delete(p.pending, pairID)
	delete(p.origin, pairID)
	p.mu.Unlock()
}

// Unpair deletes C's key and appends to the local revocation list. A revoked
// device's next handshake gets refused at hello_c verification (PROTOCOL §5.4).
func (p *Pairing) Unpair(devC string) {
	_ = p.store.Delete("mobilebridge.peer." + devC + ".pub")
	revoked, _ := p.store.Get("mobilebridge.revoked")
	entry := []byte(devC + ",")
	if !bytes.Contains(revoked, entry) {
		revoked = append(revoked, entry...)
		_ = p.store.Set("mobilebridge.revoked", revoked)
	}
	p.audit.Unpaired(devC)
}

// PeerPub returns C's stored public key if paired, else (nil,false).
func (p *Pairing) PeerPub(devC string) (ed25519.PublicKey, bool) {
	b, err := p.store.Get("mobilebridge.peer." + devC + ".pub")
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(b), true
}

// IsRevoked checks the local revocation list.
func (p *Pairing) IsRevoked(devC string) bool {
	revoked, _ := p.store.Get("mobilebridge.revoked")
	return bytes.Contains(revoked, []byte(devC+","))
}
