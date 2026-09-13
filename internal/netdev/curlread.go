package netdev

// curlread.go — curl 读表项的尾参收紧（E6，MODEL_DEPLOY_SPEC E3 修理过程中
// 发现的既有安全缺口）。前缀模型管不住 curl 的尾参：读表原收录 `curl -I `
// 前缀，词边界允许追加 -o（写文件）/-T（上传）/第二 URL——注释声称的
// "HEAD-only 无数据通道"对前缀为真、对整条命令为假。
//
// 收紧范式 = logPathReadOverride 同款（分类器旁路 + token 级语法校验）：
// curl 从前缀读表**除名**，改为整条命令逐 token 校验——flag 白名单（无参
// flag + 显式成对 flag）、URL 必须是最后一个 token 且唯一。放行面外的形态
// （-o/-T/-F/-d/-X/多 URL）一律回落分类器按 Unknown 处理；确有需要的形态走
// extra_read 由用户逐条授予（知识增长路径，NETDEV_SPEC B-1）。

import (
	"strings"

	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// curlReadFlagsNoVal: 无值 flag 白名单。全部保持 GET/HEAD 语义或纯输出修饰：
// -I/--head（HEAD 本体）、-s/-S（静默/显示错误）、-k/--insecure（跳过证书
// 校验——探活自签场景）、-i（响应含头）、-L（跟随重定向——无请求体，外联
// 面等价于目标在命令行里写死）。
var curlReadFlagsNoVal = map[string]bool{
	"-I": true, "--head": true, "-s": true, "-S": true,
	"-k": true, "--insecure": true, "-i": true, "-L": true,
}

// curlReadFlagsWithVal: 成对 flag 白名单。-H 只改请求头（无请求体）；
// 两个超时是数字——语法校验顺带做。
var curlReadFlagsWithVal = map[string]bool{
	"-H": true, "--max-time": true, "--connect-timeout": true,
}

// curlReadOverride classifies a curl command as Read when it matches the
// strict HEAD-probe grammar: `curl [safe-flags...] URL` — one URL, final
// token, no write-shaped flags anywhere. Runs after the metachar guard, so
// the line is already one plain metachar-free command.
//
// 两个语法细节：
//   - 合并短旗标（-sI/-sSk）逐字符对无值白名单校验——探活惯用形态；
//   - 成对 flag 的值含空格时（curl -H 'X: 1'）Fields 会拆成多 token，
//     值消费规则 = 连续吞掉后续非 flag token（遇到 flag 或 URL 位即停）。
func curlReadOverride(drv driver.Driver, command string) (driver.Class, bool) {
	if !driver.IsShellMetacharDriver(drv.Key()) {
		return driver.Unknown, false
	}
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "curl" {
		return driver.Unknown, false
	}
	// 轮2复核：引号不是 ShellMetachars——经 API 进来的字面引号会让 "-H \"@file\""
	// 绕过 @ 前缀检查（远端 shell 去引号后照读本地文件）。本通道审计语境
	// 不需要引号，含引号一律拒。
	for _, f := range fields[1:] {
		if strings.ContainsAny(f, "\"'") {
			return driver.Unknown, false
		}
	}
	url := fields[len(fields)-1]
	if strings.HasPrefix(url, "-") {
		// 收尾 token 是 flag 不是 URL——语法不成立（curl 也确实会报
		// "no URL specified"）。不放行。
		return driver.Unknown, false
	}
	// 轮1/轮2审查：file:/// 与 file:/path（单斜杠合法形态）都把读表语义
	// 变成本地任意读原语——scheme 提取后小写比对，只收 http(s)；无 scheme
	// 的 host[:port]/path 形态仍是远程探测（curl 默认 http），放行。
	if i := strings.Index(url, ":"); i > 0 {
		scheme := strings.ToLower(url[:i])
		if strings.Contains(scheme, "/") || strings.Contains(scheme, ".") {
			scheme = "" // "host:port" 的冒号不是 scheme 分隔
		}
		switch scheme {
		case "", "http", "https":
		default:
			return driver.Unknown, false // file/ftp/gopher/FILE… 一律拒
		}
	}
	for i := 1; i < len(fields)-1; i++ {
		f := fields[i]
		switch {
		case curlReadFlagsNoVal[f]:
			continue
		case isMergedShortReadFlags(f):
			continue
		case curlReadFlagsWithVal[f]:
			// 值消费：吞掉后续非 flag 的 middle token（quoted 空格拆分产物）。
			// 轮1审查：-H @file 是"从文件读请求头"——本地任意文件随请求头
			// 外传，@ 前缀值一律拒。轮2复核：被吞 token 含 :// 即中缀第二
			// URL（curl 会逐 URL 请求）——"URL 唯一"不变量的补丁。
			for i+1 < len(fields)-1 && !strings.HasPrefix(fields[i+1], "-") {
				i++
				if strings.HasPrefix(fields[i], "@") || strings.Contains(fields[i], "://") {
					return driver.Unknown, false
				}
			}
			continue
		}
		return driver.Unknown, false // 白名单外（含 -o/-T/-F/-d/-X 与第二 URL 位）
	}
	return driver.Read, true
}

// isMergedShortReadFlags accepts combined short flags like "-sI": a single
// dash followed by 1-4 chars all present in the no-value whitelist.
func isMergedShortReadFlags(tok string) bool {
	if len(tok) < 2 || len(tok) > 5 || tok[0] != '-' {
		return false
	}
	for _, r := range tok[1:] {
		if !curlReadFlagsNoVal["-"+string(r)] {
			return false
		}
	}
	return true
}
