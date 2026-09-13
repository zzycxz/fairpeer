package netdev

// profiles.go — 机型/模型能力档案的消费面（E4+E7，ACCEL_SPEC §8.3、GAPS 台账
// 批①）。档案是数据：accel_profiles/model_cards 都在 config（用户行），模型
// 侧另带一份内置实勘初始集（BuiltinModelCards——公开模型矩阵，不发版即查）。
// 消费方三处，全部建议性（不硬失败，沿用 D1 软降级口径）：
//  1. CheckDeployment —— TP/显存/硬件族配对校验（模板渲染与提案起草调用）
//  2. BuildGPUBoard —— readiness/特供徽标 + 档案×实测偏差告警（gpudash.go）
//  3. NetDevProfileCheck binding（desktop 层）—— 对话内部署建议可见
//
// 不承诺引擎兼容（ACCEL_SPEC §8.4 L4）：readiness 只是展示数据，最终以真机
// 验证为准（dogfooding）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zzycxz/fairpeer/internal/config"
)

// hwFamilyAccel maps hardware-family prefixes to the accel enum so a model
// card row can be cross-checked against a machine profile's accel.
var hwFamilyAccel = map[string]string{
	"nvidia-h": "nvidia", "nvidia-a": "nvidia", "nvidia-4090": "nvidia",
	"ascend-910b": "ascend", "ascend-310p": "ascend", "ascend-a3": "ascend",
	"kunlunxin-p800": "kunlunxin", "enflame-s60": "enflame", "cambricon-mlu": "cambricon",
}

// AccelProfileForDevice matches a device's Model text against the site's
// accel_profiles. Case-insensitive substring, longest SKU wins ("H20×8" should
// not lose to "H20"); accel-qualified SKUs need no separate disambiguator.
func AccelProfileForDevice(nd config.NetDevConfig, d config.NetDevDevice) *config.NetDevAccelProfile {
	model := strings.ToLower(strings.TrimSpace(d.Model))
	if model == "" {
		return nil
	}
	var best *config.NetDevAccelProfile
	for i := range nd.AccelProfiles {
		p := &nd.AccelProfiles[i]
		sku := strings.ToLower(strings.TrimSpace(p.SKU))
		if sku != "" && strings.Contains(model, sku) {
			if best == nil || len(p.SKU) > len(best.SKU) {
				best = p
			}
		}
	}
	return best
}

// ResolveModelCard resolves a (name, quant, hw_family) row: user config rows
// override the built-in surveyed set by exact case-insensitive key match.
func ResolveModelCard(nd config.NetDevConfig, name, quant, hwFamily string) (config.NetDevModelCard, bool) {
	name, quant = strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(quant))
	if name == "" || quant == "" {
		return config.NetDevModelCard{}, false
	}
	hwFamily = strings.ToLower(strings.TrimSpace(hwFamily))
	if hwFamily == "" {
		// 家族缺省：矩阵内同 (name,quant) 只有一族时可直接命中；多族（如
		// H 系 fp8 / 4090 bf16 同名）不留空——调用方用 ResolveModelCardForAccel。
		for _, c := range nd.ModelCards {
			if strings.EqualFold(c.Name, name) && strings.EqualFold(c.Quant, quant) {
				return c, true
			}
		}
		for _, c := range BuiltinModelCards() {
			if strings.EqualFold(c.Name, name) && strings.EqualFold(c.Quant, quant) {
				return c, true
			}
		}
		return config.NetDevModelCard{}, false
	}
	for _, c := range nd.ModelCards {
		if strings.EqualFold(c.Name, name) && strings.EqualFold(c.Quant, quant) && strings.EqualFold(c.HWFamily, hwFamily) {
			return c, true
		}
	}
	for _, c := range BuiltinModelCards() {
		if strings.EqualFold(c.Name, name) && strings.EqualFold(c.Quant, quant) && strings.EqualFold(c.HWFamily, hwFamily) {
			return c, true
		}
	}
	return config.NetDevModelCard{}, false
}

// accelDefaultFamily is the primary hw_family per accel enum (the surveyed
// mainstream silicon): nvidia→H 系、ascend→910B、kunlunxin→P800 …
var accelDefaultFamily = map[string]string{
	"nvidia": "nvidia-h", "ascend": "ascend-910b", "kunlunxin": "kunlunxin-p800",
	"enflame": "enflame-s60", "cambricon": "cambricon-mlu",
}

// ResolveModelCardForAccel resolves (name, quant) against the machine's accel
// family default — the natural pairing when the caller thinks in machines, not
// families.
func ResolveModelCardForAccel(nd config.NetDevConfig, accel, name, quant string) (config.NetDevModelCard, bool) {
	return ResolveModelCard(nd, name, quant, accelDefaultFamily[accel])
}

// bytesPerParam estimates weight bytes per parameter by quant tier. Unknown
// tiers estimate conservatively at BF16 (2 bytes) — over-estimating weights
// only makes the advisory stricter, never looser.
func bytesPerParam(quant string) float64 {
	switch quant {
	case "fp8", "w8a8", "w8a8c16", "int8":
		return 1
	case "awq", "gptq", "int4":
		return 0.5
	default: // bf16, fp16, "" and anything new
		return 2
	}
}

// EstimateWeightsGB returns the model-weights VRAM estimate for one card row:
// explicit override wins, else params_b × bytes/param（BF16≈2GB/B 实勘规则）.
func EstimateWeightsGB(c config.NetDevModelCard) float64 {
	if c.VRAMGB > 0 {
		return c.VRAMGB
	}
	return c.ParamsB * bytesPerParam(c.Quant)
}

// CheckDeployment runs the advisory (never fatal) deployment checks for one
// node profile + one model card. tp is the tensor-parallel size declared for
// the node. Returned strings are Chinese advisory lines meant for UI/Note
// surfaces; an empty slice means no advice (not a blessing — see §8.4 L4).
func CheckDeployment(p *config.NetDevAccelProfile, card config.NetDevModelCard, tp int) []string {
	if p == nil {
		return []string{"未配置机型能力档案（[[netdev.accel_profiles]]），跳过机型侧校验——建议先建档"}
	}
	if tp <= 0 {
		return []string{"TP 未声明，跳过部署校验"}
	}
	var out []string
	if tp > p.Cards && p.Cards > 0 {
		out = append(out, fmt.Sprintf("TP=%d 超过机型 %s 常见卡数 %d——跨机 TP 需要专用互联（超节点/NVLink 域），单机内请核对", tp, p.SKU, p.Cards))
	}
	if tp&(tp-1) != 0 || tp > 8 {
		out = append(out, fmt.Sprintf("TP=%d 不是常见布局（1/2/4/8）——引擎按 tp 世界切分注意力头，非常见布局需真机验证", tp))
	}
	// 硬件族×机型 accel 配对：模型档案行的硬件族必须与机型 accel 同族。
	if accel, ok := hwFamilyAccel[card.HWFamily]; ok && accel != p.Accel {
		out = append(out, fmt.Sprintf("模型档案硬件族 %s 与机型 accel=%s 不同族——该量化档在此机型无实勘依据", card.HWFamily, p.Accel))
	}
	// 权重显存 vs TP 卡显存：BF16≈2GB/B 规则估算；>85% 即提示 KV 余量紧张
	// （KV cache、激活与 CUDA 图都要在同一池子里）。
	weights := EstimateWeightsGB(card)
	if p.VRAMGB > 0 {
		budget := float64(tp) * p.VRAMGB
		if budget > 0 {
			if weights > budget {
				out = append(out, fmt.Sprintf("%s（%s，权重≈%.0fGB）超出 %d×%s（%.0fGB）——量化降档或扩 TP/台数", card.Name, strings.ToUpper(card.Quant), weights, tp, p.SKU, budget))
				if perNode := float64(p.Cards) * p.VRAMGB; p.Cards > 0 && perNode > 0 {
					need := int(weights/perNode) + 1
					if need > 1 {
						out = append(out, fmt.Sprintf("估算需 ≥%d 台 %d 卡该机型（含整卡并行布局）", need, p.Cards))
					}
				}
			} else if weights > budget*0.85 {
				out = append(out, fmt.Sprintf("权重≈%.0fGB 已占 %d×%s（%.0fGB）的 85%%+——KV 余量紧张，长上下文请降 TP 占比或量化", weights, tp, p.SKU, budget))
			}
		}
	}
	if card.ActiveB > 0 {
		out = append(out, fmt.Sprintf("MoE 模型（激活 %.0fB/%.0fB）——显存放得下≠吞吐够，容量规划按激活参数估吞吐", card.ActiveB, card.ParamsB))
	}
	switch p.Readiness {
	case "experimental":
		out = append(out, fmt.Sprintf("机型 %s 引擎 readiness=experimental——上线前必须真机验证（%s）", p.SKU, p.Notes))
	case "new":
		out = append(out, fmt.Sprintf("机型 %s 引擎 readiness=new——建议先在灰度节点验证", p.SKU))
	}
	if p.Special != "" {
		out = append(out, fmt.Sprintf("机型 %s 标注 %s——特供/裁剪 SKU 的算力上限以规格页为准", p.SKU, p.Special))
	}
	return out
}

// builtinModelRow is the compact surveyed form: one model × the families it
// ships on, expanded into per-family quant rows by BuiltinModelCards.
type builtinModelRow struct {
	name    string
	paramsB float64
	activeB float64 // 0 = dense
	vramGB  float64 // 显式权重估算覆盖（0 = 按量化估）
	notes   string
	// families lists the hw_family keys this model ships on; rows inherit the
	// family-default quant ladder (familyQuantLadder) unless overridden.
	families []string
	// quantOverride swaps the default quant ladder for this model (rare).
	quantOverride map[string]string
}

// familyQuantLadder is the E7 surveyed per-family primary-quant order (first
// = 主流量化档): H 系 FP8 原生、910B W8A8（950 前无 FP8）、P800 W8A8C16、
// A 系/4090 BF16+AWQ。
var familyQuantLadder = map[string][]string{
	"nvidia-h":       {"fp8", "bf16"},
	"nvidia-a":       {"bf16", "awq"},
	"nvidia-4090":    {"bf16", "awq"},
	"ascend-910b":    {"w8a8", "bf16"},
	"ascend-310p":    {"w8a8"},
	"ascend-a3":      {"w8a8", "bf16"},
	"kunlunxin-p800": {"w8a8c16", "bf16"},
	"enflame-s60":    {"bf16"},
	"cambricon-mlu":  {"bf16"},
}

// smallFamilies is the family set small (≤14B) models run on — everything
// with a mainstream engine path.
var smallFamilies = []string{
	"nvidia-h", "nvidia-a", "nvidia-4090",
	"ascend-910b", "ascend-310p", "ascend-a3",
	"kunlunxin-p800", "enflame-s60", "cambricon-mlu",
}

// builtinModelMatrix is the E7 surveyed initial set (2026-09 实勘 45 条校准):
// Qwen2.5 全档 + Qwen3 MoE 系 + DeepSeek V3/R1 + R1-Distill 蒸馏系（政企一体
// 机主力）+ GLM-4.5/-Air + MiniMax-M1 + K2（标 API）+ VLM 两行 + Llama（存量
// 蒸馏底座）。新模型=用户 config 加行，不进内置表也完全可用。
var builtinModelMatrix = []builtinModelRow{
	{name: "qwen2.5-0.5b", paramsB: 0.5, families: smallFamilies},
	{name: "qwen2.5-1.5b", paramsB: 1.5, families: smallFamilies},
	{name: "qwen2.5-3b", paramsB: 3, families: smallFamilies},
	{name: "qwen2.5-7b", paramsB: 7, families: smallFamilies},
	{name: "qwen2.5-14b", paramsB: 14, families: smallFamilies},
	{name: "qwen2.5-32b", paramsB: 32, families: []string{"nvidia-h", "nvidia-a", "nvidia-4090", "ascend-910b", "ascend-a3", "kunlunxin-p800", "cambricon-mlu"}},
	{name: "qwen2.5-72b", paramsB: 72, families: []string{"nvidia-h", "nvidia-a", "nvidia-4090", "ascend-910b"}},
	{name: "qwen3-30b-a3b", paramsB: 30, activeB: 3, notes: "智算中心单机标配（E7 实勘口径）", families: []string{"nvidia-h", "nvidia-4090", "ascend-910b", "kunlunxin-p800", "cambricon-mlu"}},
	{name: "qwen3-235b-a22b", paramsB: 235, activeB: 22, families: []string{"nvidia-h", "ascend-910b"}},
	{name: "qwen3-next-80b-a3b", paramsB: 80, activeB: 3, families: []string{"nvidia-h"}},
	{name: "deepseek-v3", paramsB: 671, activeB: 37, families: []string{"nvidia-h", "ascend-910b"}, notes: "昇腾官方口径：BF16≥4台A2、W8A8≥2台"},
	{name: "deepseek-r1", paramsB: 671, activeB: 37, families: []string{"nvidia-h", "ascend-910b"}, notes: "推理版权重=V3 同体量；昇腾口径同 V3"},
	{name: "deepseek-r1-distill-qwen-1.5b", paramsB: 1.5, families: smallFamilies, notes: "政企一体机主力蒸馏系"},
	{name: "deepseek-r1-distill-qwen-7b", paramsB: 7, families: smallFamilies},
	{name: "deepseek-r1-distill-qwen-14b", paramsB: 14, families: smallFamilies},
	{name: "deepseek-r1-distill-qwen-32b", paramsB: 32, families: []string{"nvidia-h", "nvidia-a", "nvidia-4090", "ascend-910b", "ascend-a3", "kunlunxin-p800"}},
	{name: "deepseek-r1-distill-llama-70b", paramsB: 70, families: []string{"nvidia-h", "nvidia-a", "ascend-910b"}},
	{name: "glm-4.5", paramsB: 355, activeB: 32, families: []string{"nvidia-h", "ascend-910b"}},
	{name: "glm-4.5-air", paramsB: 106, activeB: 12, notes: "轻量档（106B）", families: []string{"nvidia-h", "nvidia-4090", "ascend-910b"}},
	{name: "minimax-m1", paramsB: 456, activeB: 45.9, notes: "8×H800 可部署", families: []string{"nvidia-h"}},
	{name: "kimi-k2", paramsB: 1000, activeB: 32, families: []string{"nvidia-h"}, notes: "官方最小 16×H200；多走 API 少自建"},
	{name: "qwen2.5-vl-7b", paramsB: 7, vramGB: 16, notes: "VLM：权重含视觉塔增量", families: smallFamilies},
	{name: "internvl2.5-8b", paramsB: 8, vramGB: 18, notes: "VLM：权重含视觉塔增量", families: smallFamilies},
	{name: "llama-3.1-70b", paramsB: 70, families: []string{"nvidia-h", "nvidia-a"}, notes: "存量蒸馏底座（占比下降）"},
}

// BuiltinModelCards expands the surveyed matrix into flat config-shaped rows.
// Sorted by name then family for stable display/diff. These are advisory
// estimates for capacity conversation — never a compatibility promise.
func BuiltinModelCards() []config.NetDevModelCard {
	out := make([]config.NetDevModelCard, 0, len(builtinModelMatrix)*3)
	for _, m := range builtinModelMatrix {
		for _, fam := range m.families {
			quants := familyQuantLadder[fam]
			if q, ok := m.quantOverride[fam]; ok {
				quants = []string{q}
			}
			for _, q := range quants {
				out = append(out, config.NetDevModelCard{
					Name: m.name, ParamsB: m.paramsB, ActiveB: m.activeB,
					Quant: q, HWFamily: fam, VRAMGB: m.vramGB, Notes: m.notes,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].HWFamily != out[j].HWFamily {
			return out[i].HWFamily < out[j].HWFamily
		}
		return out[i].Quant < out[j].Quant
	})
	return out
}
