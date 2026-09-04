# convert_nvd2.py — NVD API 2.0 响应 → 本应用简化 feed 格式。
# 用法: python convert_nvd2.py [nvd2-raw.json]（默认用 curl 拉最近 90 天 CRITICAL）。
# 产品提取规则与 internal/netdev/cve.go 的 cpeProducts 一致：CPE 2.3 URI 的
# 厂商 + 产品（下划线转空格），vulnerable=false 排除，每条最多 12 个；
# 无 CPE 的条目丢弃（无法匹配清单）。
import json
import sys

RAW = "nvd2-critical-90d-raw.json"
OUT = "nvd2-critical-90d-simple.json"


def cpe_products(cpe: str) -> list[str]:
    f = cpe.split(":")
    if len(f) < 5:
        return []
    out = []

    def add(s: str) -> None:
        s = s.replace("\\", "").lower()
        if len(s) < 2 or s in ("-", "*"):
            return
        out.append(s)

    add(f[3])
    add(f[4].replace("_", " "))
    return out


def severity(v: dict) -> str:
    m = v.get("metrics", {})
    for key in ("cvssMetricV31", "cvssMetricV30", "cvssMetricV2"):
        ms = m.get(key) or []
        if ms and ms[0].get("cvssData", {}).get("baseSeverity"):
            return ms[0]["cvssData"]["baseSeverity"].strip().lower()
    return ""


def en_desc(v: dict) -> str:
    for d in v.get("descriptions", []):
        if d.get("lang") == "en" and d.get("value", "").strip():
            return d["value"]
    ds = v.get("descriptions", [])
    return ds[0]["value"] if ds else ""


def convert(raw: dict) -> list[dict]:
    out = []
    for it in raw.get("vulnerabilities", []):
        v = it.get("cve", {})
        cid = v.get("id", "").strip()
        if not cid:
            continue
        seen: set[str] = set()
        products: list[str] = []
        for cfg in v.get("configurations", []):
            for nd in cfg.get("nodes", []):
                for cm in nd.get("cpeMatch", []):
                    if cm.get("vulnerable") is False:
                        continue
                    for p in cpe_products(cm.get("criteria", "")):
                        if p not in seen and len(products) < 12:
                            seen.add(p)
                            products.append(p)
        if not products:
            continue
        out.append({
            "id": cid,
            "desc": en_desc(v)[:300],
            "products": products,
            "severity": severity(v) or "high",
        })
    return out


def main() -> None:
    path = sys.argv[1] if len(sys.argv) > 1 else RAW
    with open(path, encoding="utf-8") as f:
        raw = json.load(f)
    entries = convert(raw)
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump({"cves": entries}, f, ensure_ascii=False, separators=(",", ":"))
    print(f"{OUT}: {len(entries)} entries (raw totalResults={raw.get('totalResults')})")


if __name__ == "__main__":
    main()
