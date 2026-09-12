package netdev

// gpudash.go — DashShell 第六屏「智算」的聚合板（FDE_AIINFRA gap §4.1-6，
// 批次 C 前置：纯只读汇编，数据面 = gpuhealth.go 的健康段 + 时序面 + XID
// Finding）。零新探针： healthState 快照已带每卡温度/显存/利用率与 XID，
// series 已有 gpu.<index>.<metric> 历史。
//
// UI 契约（NETDEV_SPEC_V2 §10）：作为 DashShell 内部新屏（合法大屏 chip），
// 不新增 dock 页签或第四工作台。

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// gpuSparkCap bounds each trend series (24h downsampled).
const gpuSparkCap = 48

// GPUBoardCard is one card's latest row in the 卡×指标 matrix.
type GPUBoardCard struct {
	Index      int    `json:"index"`
	Name       string `json:"name,omitempty"`
	TempC      int    `json:"tempC,omitempty"`
	UtilPct    int    `json:"utilPct,omitempty"`
	MemPct     int    `json:"memPct,omitempty"`
	MemUsedMB  uint64 `json:"memUsedMB,omitempty"`
	MemTotalMB uint64 `json:"memTotalMB,omitempty"`
	XIDMax     int    `json:"xidMax,omitempty"` // 本轮该卡相关 XID（设备级归并，见 GPUBoardDevice）
}

// GPUBoardDevice is one GPU host's block on the board.
type GPUBoardDevice struct {
	Device     string         `json:"device"`
	Reachable  bool           `json:"reachable"`
	GPUSampled bool           `json:"gpuSampled"`
	GPUOnly    bool           `json:"gpuOnly,omitempty"`
	XIDMax     int            `json:"xidMax,omitempty"` // 设备级本轮最大码（0 = 无）
	Cards      []GPUBoardCard `json:"cards"`
	LastError  string         `json:"lastError,omitempty"`
	// TempSpark：24h 全卡最高温趋势（≤48 点降采样）——热图行的迷你趋势。
	TempSpark [][2]float64 `json:"tempSpark,omitempty"`
}

// GPUBoardXID is one XID finding on the event stream.
type GPUBoardXID struct {
	ID       string `json:"id"`
	Device   string `json:"device"`
	MaxCode  int    `json:"maxCode,omitempty"`
	Severity string `json:"severity"`
	At       string `json:"at,omitempty"`
	Active   bool   `json:"active"`
}

// GPUBoard is the 智算 screen snapshot.
type GPUBoard struct {
	GeneratedAt  string           `json:"generated_at"`
	Devices      []GPUBoardDevice `json:"devices"`
	TotalCards   int              `json:"total_cards"`
	SampledDevs  int              `json:"sampled_devices"` // 本轮真采到数据的主机数
	WorstTemp    int              `json:"worst_temp,omitempty"`
	WorstTempDev string           `json:"worst_temp_dev,omitempty"`
	XIDActive    int              `json:"xid_active"` // 活动 XID Finding 数
	XIDEvents    []GPUBoardXID    `json:"xid_events"`
}

// BuildGPUBoard assembles the 智算 screen from existing GPU health snapshots,
// the series store (temp trend), and XID findings. Zero probes.
func (m *Manager) BuildGPUBoard() *GPUBoard {
	b := &GPUBoard{
		GeneratedAt: time.Now().Format("01-02 15:04"),
		Devices:     []GPUBoardDevice{},
		XIDEvents:   []GPUBoardXID{},
	}
	snap := m.HealthSnapshot()
	for _, h := range snap.Devices {
		// GPU 语义判定以【清单标记】为准：GPU=true 的设备全态上板（含
		// "尚未轮询"/"采集未接入"占位卡——灰显成因，值班不猜）；纯网络
		// SNMP 设备不上智算屏。
		if d, ok := m.cfg.NetDevDeviceByName(h.Device); !ok || !d.GPU {
			continue
		}
		dev := GPUBoardDevice{
			Device: h.Device, Reachable: h.Reachable, GPUSampled: h.GPUSampled,
			GPUOnly: h.GPUOnly, XIDMax: h.GPUXIDMax,
			Cards: []GPUBoardCard{},
		}
		// 失败成因合并：GPU 侧优先（GPULastError）；不可达且 GPU 侧无注记时
		// 回落 SNMP 侧 LastError——占位卡（未接入/尚未轮询）的成因在那里。
		dev.LastError = h.GPULastError
		if dev.LastError == "" && !h.Reachable {
			dev.LastError = h.LastError
		}
		for _, c := range h.GPU {
			dev.Cards = append(dev.Cards, GPUBoardCard{
				Index: c.Index, Name: c.Name, TempC: c.TempC, UtilPct: c.UtilPct,
				MemPct: c.MemPct(), MemUsedMB: c.MemUsedMB, MemTotalMB: c.MemTotalMB,
				XIDMax: h.GPUXIDMax,
			})
			b.TotalCards++
			if c.TempC > b.WorstTemp {
				b.WorstTemp, b.WorstTempDev = c.TempC, h.Device
			}
		}
		if h.GPUSampled {
			b.SampledDevs++
		}
		dev.TempSpark = gpuTempSpark(h.Device)
		b.Devices = append(b.Devices, dev)
	}
	// 采集器没标注但确实在跑的主机不误报——SampledDevs 只数 GPU 段非空的行
	// 已在上面处理；这里按状态兜底计数。
	sort.Slice(b.Devices, func(i, j int) bool { return b.Devices[i].Device < b.Devices[j].Device })

	// XID 事件流：活动 Finding + 24h 内已解决的（事件视角，不只是队列）。
	fs, err := ListFindings()
	if err == nil {
		cutoff := time.Now().Add(-24 * time.Hour)
		for _, f := range fs {
			if !strings.HasPrefix(f.Source, "gpu:xid:") {
				continue
			}
			if f.Status != "active" && f.CreatedAt.Before(cutoff) {
				continue
			}
			ev := GPUBoardXID{
				ID: f.ID, Device: strings.Join(f.Devices, ","), Severity: f.Severity,
				At: f.CreatedAt.Format("01-02 15:04"), Active: f.Status == "active",
			}
			if f.Status == "active" {
				b.XIDActive++
			}
			fmt.Sscanf(f.Title, "[GPU] XID 事件 #%d", &ev.MaxCode)
			b.XIDEvents = append(b.XIDEvents, ev)
		}
	}
	return b
}

// gpuTempSpark downsamples the 24h all-card max-temperature trend for one
// device to ≤gpuSparkCap points ([unixSec, tempC]).
func gpuTempSpark(device string) [][2]float64 {
	series := SeriesRead(device, 24*time.Hour)
	// 归并全卡温度：按 30 分钟桶取最大值。
	type bucket struct {
		t   int64
		max float64
	}
	buckets := map[int64]*bucket{}
	for metric, pts := range series {
		if !strings.HasPrefix(metric, "gpu.") || !strings.HasSuffix(metric, ".temp") {
			continue
		}
		for _, pt := range pts {
			b := pt.T / 1800 // 30min
			bk, ok := buckets[b]
			if !ok {
				bk = &bucket{t: b * 1800}
				buckets[b] = bk
			}
			if pt.Value > bk.max {
				bk.max = pt.Value
			}
		}
	}
	if len(buckets) == 0 {
		return nil
	}
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([][2]float64, 0, gpuSparkCap)
	step := 1
	if len(keys) > gpuSparkCap {
		step = (len(keys) + gpuSparkCap - 1) / gpuSparkCap
	}
	for i := 0; i < len(keys); i += step {
		bk := buckets[keys[i]]
		out = append(out, [2]float64{float64(bk.t), bk.max})
	}
	return out
}
