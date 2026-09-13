package netdev

import (
	"testing"

	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

func curlOverride(t *testing.T, cmd string) (driver.Class, bool) {
	t.Helper()
	drv, ok := driver.For("linux", "")
	if !ok {
		t.Fatal("no linux driver")
	}
	return curlReadOverride(drv, cmd)
}

func TestCurlReadOverride(t *testing.T) {
	reads := []string{
		"curl -I http://127.0.0.1:8000/health",
		"curl -I 127.0.0.1:8000/health",           // 无 scheme 也收
		"curl -sI http://127.0.0.1:8000/health",   // 合并短旗标
		"curl -sSkL http://h/health",              // 四连合并
		"curl -I -k https://self-signed/health",   // 自签探活
		"curl --head http://10.0.0.1:443",         // 长形态
		"curl -I -H 'X-Debug: 1' http://h/health", // 头部成对 flag（值含空格拆多 token）
		"curl -I --max-time 5 http://h/health",    // 超时成对 flag
		"curl -sS -L -k http://h/health",          // 输出修饰组合
	}
	for _, c := range reads {
		if cls, ok := curlOverride(t, c); !ok || cls != driver.Read {
			t.Errorf("read %q -> ok=%v class=%v", c, ok, cls)
		}
	}
	refused := []string{
		"curl -I http://h/health -o /tmp/out",    // 尾参写文件（E6 原始缺口）
		"curl -o /etc/passwd http://h/health",    // 中段覆盖写
		"curl -I -T weights.bin http://h/upload", // 上传
		"curl -I -F @file http://h/form",         // 表单上传
		"curl -d {\"x\":1} http://h/api",         // 请求体
		"curl -I -X POST http://h/api",           // 方法改写——HEAD 变 POST
		"curl -I http://h/a http://h/b",          // 第二 URL
		"curl --output /tmp/o http://h/health",   // 长形态写文件
		"curl -I",                                // 缺 URL
		"curl -I --max-time",                     // 值位/URL 位缺失
		"curl http://h/health -- foo",            // URL 不在收尾位
		"curl -H x -o /tmp/evil http://h/health", // 值消费后仍须验其余 flag
	}
	for _, c := range refused {
		if cls, ok := curlOverride(t, c); ok && cls == driver.Read {
			t.Errorf("mutating %q classified READ", c)
		}
	}
	// 非 shell 驱动不适用（网络 CLI 无 curl 语义）。
	d, ok := driver.For("huawei", "")
	if !ok {
		t.Skip("no network CLI driver available")
	}
	if _, ok := curlReadOverride(d, "curl -I http://h"); ok {
		t.Error("non-shell driver must not take the curl override")
	}
}

// TestCurlRemovedFromPrefixTable 锁死 E6 的读表除名：前缀分类器对任何 curl
// 形态不再直接给 Read（探活只能走 curlReadOverride 语法校验）。
func TestCurlRemovedFromPrefixTable(t *testing.T) {
	drv, ok := driver.For("linux", "")
	if !ok {
		t.Fatal("no linux driver")
	}
	for _, c := range []string{
		"curl -I http://h/health",
		"curl -I http://h/health -o /tmp/out",
	} {
		if drv.Classify(c) == driver.Read {
			t.Errorf("prefix table still grants READ to %q — E6 regression", c)
		}
	}
}
