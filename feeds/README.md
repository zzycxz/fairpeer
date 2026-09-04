# CVE 情报源（自备，不随产品分发）

供桌面端「网络运维 → 安全工作台 → CVE」页签粘贴导入。产物均为应用简化格式
`{"cves":[{"id","desc","products":[],"severity"}]}`（`products` 为小写厂商/产品子串，
对清单的 厂商+系统+型号 做包含匹配）。

## 产物清单

| 文件 | 条数 | 内容 | 生成方式 |
|---|---|---|---|
| `kev-simple-full.json` | ~1700 | CISA KEV 全量（在野利用漏洞） | `python convert_kev.py` |
| `kev-infra.json` | ~900 | KEV 的基础设施/网络设备/服务器子集（推荐首选） | 同上（脚本内 INFRA_TOKENS 过滤） |
| `nvd2-critical-90d-simple.json` | ~400 | NVD 最近 90 天 CVSS critical | 见下方 curl + `python convert_nvd2.py` |
| `qianxin-oneday-simple.json` | ~250 | 奇安信威胁情报中心当日增量（极危/高危） | `python convert_qianxin.py`（免鉴权） |
| `kev-raw.json` / `nvd2-critical-90d-raw.json` / `qianxin-oneday.json` | - | 原始下载（KEV 原生 / NVD API 2.0 原生 / 奇安信原生，均可再利用） | - |

> 注意：`qianxin-oneday` 是**每日增量**（当天新增+更新），积累库存需每天跑一次把
> 产物合并保存；或直接部署 [watchvuln](https://github.com/zema1/watchvuln) 常驻采集。

## 刷新命令

```bash
# KEV（转换脚本会自动下载再转换，也可手动）：
curl -sSL -o kev-raw.json https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json
python convert_kev.py kev-raw.json

# NVD API 2.0（注意：severity 值必须大写，小写返回 404）：
curl -sS --get "https://services.nvd.nist.gov/rest/json/cves/2.0" \
  --data-urlencode "cvssV3Severity=CRITICAL" \
  --data-urlencode "pubStartDate=$(date -u -d '-90 days' +%Y-%m-%dT00:00:00.000+00:00)" \
  --data-urlencode "pubEndDate=$(date -u +%Y-%m-%dT00:00:00.000+00:00)" \
  --data-urlencode "resultsPerPage=2000" -o nvd2-critical-90d-raw.json
python convert_nvd2.py
```

浏览器直接粘贴也可：`https://services.nvd.nist.gov/rest/json/cves/2.0?keywordSearch=cisco&resultsPerPage=200`
的响应就是应用原生支持的 NVD 2.0 格式，无需转换。

## 来源说明

### 国内

- **奇安信威胁情报中心** <https://ti.qianxin.com>：one-day 接口
  `POST https://ti.qianxin.com/alpha-api/v2/vuln/one-day` 免鉴权，返回当日
  新增/更新漏洞（含中英文标题、CVE/CNVD/CNNVD 编号、极危/高危等级、影响面标签）。
  转换脚本 `convert_qianxin.py`，产品名从英文标题前缀提取。
- **OSCS 开源安全情报** <https://www.oscs1024.com>：
  `POST https://www.oscs1024.com/oscs/v1/intelligence/list`（body `{"page":1,"per_page":20}`，
  需带 OSCS 的 Referer/Origin 头）实测可用，共 1600+ 条；偏开源软件供应链
  （WordPress/Jenkins/Apache 等），标题为中文，暂未写转换脚本。
- **watchvuln** <https://github.com/zema1/watchvuln>：开源聚合器，常驻运行可
  聚合阿里云 AVD / 奇安信 / OSCS / 微步 / 长亭 / KEV 等源并去重推送，本地建
  全量库。要长期自动化时优先用它，再从它的库导出转换。
- **CNVD** <https://www.cnvd.org.cn>（国家漏洞共享平台）/ **CNNVD**
  <https://www.cnnvd.org.cn>（国家漏洞库）：最权威的国内官方库，但均无开放
  API——CNVD 对脚本访问直接拒绝（HTTP 521），CNNVD 数据下载需注册登录（XML
  格式，见 [collect-cnnvd-vuln 文档](https://github.com/y4ney/collect-cnnvd-vuln)）。
  适合人工查证，不适合自动 feed。
- **阿里云漏洞库 AVD** <https://avd.aliyun.com>：网页免费可查，但接口有 WAF
  （watchvuln 专门写了绕过逻辑），且无独立免费 API；数据已被奇安信/OSCS 路径覆盖。
- **华为 PSIRT** <https://www.huawei.com/en/psirt/all-bulletins>：页面为 JS 壳，
  curl 拿不到数据；华为设备的漏洞情报走厂商公告人工跟踪最可靠。

### 国际（权威）

- **CISA KEV** <https://www.cisa.gov/known-exploited-vulnerabilities-catalog>：
  在野利用清单，量少权重高。转换规则：勒索软件在用 → critical，其余 → high。
  注意 KEV 不收录华为/H3C/锐捷等国产厂商（2026-09 核实为 0 条），国产设备
  漏洞情报需走厂商安全公告 / CNVD / 奇安信。
- **NVD API 2.0** <https://nvd.nist.gov>：按 `keywordSearch`（关键词）、
  `virtualMatchString`（CPE 限定厂商）、`pubStartDate/pubEndDate`（时间段）过滤拉取。
  无 key 限速约 30 秒 5 次。产品匹配面从 CPE 提取，无 CPE 的条目被丢弃。
- **GitHub 镜像** <https://github.com/fkie-cad/nvd-json-data-feeds>：
  NVD 官方 JSON feed 停更后的社区重建（每 2 小时同步），按年份提供 2.0/1.1
  格式整库 JSON，适合脚本化切片，单文件几十 MB 不适合直接粘贴。
- **Exploit-DB** <https://www.exploit-db.com>（GitHub: offensive-security/exploitdb）：
  是漏洞利用代码索引（CSV 为 `files_exploits.csv`），**不是** CVE feed，无
  severity/产品结构，不能直接导入；用途是查命中 CVE 是否有公开利用代码，
  辅助修复优先级排序。

## 备注

- 本目录不入库（git 未跟踪），属于本机自备数据。
- 匹配是 厂商/型号 子串粗筛，不是精确版本比对；命中后须人工核对版本范围。
- 导入即整体替换缓存（`cves.json`），不是增量合并；换 feed 重新粘贴导入即可。
