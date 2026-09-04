# convert_kev.py — CISA KEV → 本应用简化 feed 格式（{"cves":[{id,desc,products,severity}]}）。
# 用法: python convert_kev.py [kev-raw.json]（默认下载最新 KEV 到 kev-raw.json 再转换）。
# 规则: products = [vendorProject, product]（小写子串，匹配清单的 厂商+系统+型号）；
#       severity = 勒索软件在用 → critical，其余 KEV 条目 → high（KEV 全部为在野利用）。
import json
import sys
import urllib.request

KEV_URL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"

# 基础设施/网络设备/服务器操作系统相关的厂商与产品关键词（小写子串匹配）。
INFRA_TOKENS = [
    "cisco", "huawei", "juniper", "fortinet", "palo alto", "zyxel", "mikrotik",
    "d-link", "dlink", "tp-link", "netgear", "aruba", "dell", "hpe", "hp ", "lenovo",
    "vmware", "citrix", "f5", "big-ip", "sonicwall", "sophos", "watchguard", "barracuda",
    "qnap", "synology", "draytek", "ivanti", "pulse secure", "fortigate", "firewall",
    "router", "switch", "vpn", "microsoft", "windows", "linux", "ubuntu", "debian",
    "red hat", "centos", "suse", "freebsd", "oracle", "sun", "apache", "nginx", "openssl",
    "openssh", "sap", "vmware", "docker", "kubernetes",
]


def load_kev(path: str | None) -> dict:
    if path:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    print(f"downloading {KEV_URL} ...")
    with urllib.request.urlopen(KEV_URL) as r:
        return json.loads(r.read().decode("utf-8"))


def to_entries(kev: dict) -> list[dict]:
    out = []
    for v in kev["vulnerabilities"]:
        ven = (v.get("vendorProject") or "").strip().lower()
        prod = (v.get("product") or "").strip().lower()
        products = [p for p in dict.fromkeys([ven, prod]) if len(p) >= 2]
        if not products:
            continue
        sev = "critical" if v.get("knownRansomwareCampaignUse") == "Known" else "high"
        desc = (v.get("shortDescription") or v.get("vulnerabilityName") or "").strip()[:300]
        out.append({
            "id": v["cveID"],
            "desc": f"{desc} [KEV exploited-in-the-wild]",
            "products": products,
            "severity": sev,
        })
    return out


def is_infra(e: dict) -> bool:
    hay = " ".join(e["products"])
    return any(tok in hay for tok in INFRA_TOKENS)


def dump(name: str, entries: list[dict]) -> None:
    with open(name, "w", encoding="utf-8") as f:
        json.dump({"cves": entries}, f, ensure_ascii=False, separators=(",", ":"))
    print(f"{name}: {len(entries)} entries")


def main() -> None:
    kev = load_kev(sys.argv[1] if len(sys.argv) > 1 else None)
    entries = to_entries(kev)
    dump("kev-simple-full.json", entries)
    dump("kev-infra.json", [e for e in entries if is_infra(e)])


if __name__ == "__main__":
    main()
