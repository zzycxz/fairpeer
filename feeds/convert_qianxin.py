# convert_qianxin.py — 奇安信威胁情报中心 one-day API → 本应用简化 feed。
# 用法: python convert_qianxin.py [qianxin-oneday.json]（默认实时拉取）。
# 接口为每日增量（vuln_add/vuln_update/key_vuln_add），要积累库存需每天跑一次合并；
# 或直接用 watchvuln（github.com/zema1/watchvuln）做常驻采集。
# 产品提取：英文标题以产品名开头（如 "TOTOLINK T6 unauthorized firmware upgrade
# vulnerability"），截到漏洞类型词为止；同时保留首词做厂商级子串，扩大命中面。
import json
import re
import sys
import urllib.request

API = "https://ti.qianxin.com/alpha-api/v2/vuln/one-day"
OUT = "qianxin-oneday-simple.json"

SEV = {"极危": "critical", "高危": "high", "中危": "medium", "低危": "low"}

# 英文标题中标志"漏洞描述开始"的常见词，截断后剩下的前缀即产品名。
TYPE_WORDS = [
    "unauth", "unrestricted", "remote code execution", "code execution",
    "command execution", "command injection", "sql injection", "buffer overflow",
    "stack overflow", "heap overflow", "use-after-free", "denial of service",
    "information disclosure", "sensitive data", "privilege escalation",
    "elevation of privilege", "security bypass", "authentication bypass",
    "access control", "path traversal", "directory traversal", "arbitrary file",
    "cross-site request", "cross-site scripting", "csrf", "xss", "ssrf", "rfi",
    "lfi", "xxe", "deserialization", "out-of-bounds", "integer overflow",
    "server-side request", "client-side", "request forgery", "open redirect",
    "format string", "race condition", "null pointer", "trust boundary",
    "insufficient", "incorrect", "improper", "insecure", "vulnerability",
    "flaw", "issue", "weakness", "bug",
]
TYPE_RE = re.compile("|".join(TYPE_WORDS), re.IGNORECASE)

# 提取后仍不像产品名的噪声前缀。
NOISE = re.compile(r"^(cve-\d+-\d+|unspecified|multiple|various|some)\b", re.IGNORECASE)


def fetch() -> dict:
    req = urllib.request.Request(API, method="POST", data=b"", headers={
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
        "Content-Type": "application/json",
    })
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode("utf-8"))


def products_from_title(title_en: str) -> list[str]:
    t = re.sub(r"\((?:CVE|QVD|CNVD|CNNVD)[^)]*\)", "", title_en).strip()
    m = TYPE_RE.search(t)
    if m:
        t = t[: m.start()].strip()
    t = t.strip(" -_:;,.")
    words = t.split()
    while words and len(words[-1]) <= 1:
        words.pop()
    if not words or NOISE.match(" ".join(words)):
        return []
    out = [" ".join(words).lower()]
    if len(words) > 1:
        out.append(words[0].lower())
    return [p for p in dict.fromkeys(out) if len(p) >= 2][:2]


def convert(d: dict) -> list[dict]:
    data = d.get("data", {})
    raw = data.get("key_vuln_add", []) + data.get("vuln_add", []) + data.get("vuln_update", [])
    seen, out = set(), []
    for v in raw:
        cid = (v.get("cve_code") or v.get("qvd_code") or "").strip()
        if not cid or cid in seen:
            continue
        seen.add(cid)
        sev = SEV.get((v.get("rating_level") or "").strip(), "high")
        if sev not in ("critical", "high"):
            continue  # 与 watchvuln 同策略：只收 极危/高危
        prods = products_from_title(v.get("vuln_name_en") or "")
        if not prods:
            continue
        tags = " ".join(tg.get("name", "") for tg in v.get("tag") or [])
        desc = (v.get("description_en") or v.get("vuln_name_en") or "").strip()[:300]
        if tags:
            desc = f"{desc} [{tags}] [奇安信TI]"
        else:
            desc = f"{desc} [奇安信TI]"
        out.append({"id": cid, "desc": desc, "products": prods, "severity": sev})
    return out


def main() -> None:
    path = sys.argv[1] if len(sys.argv) > 1 else None
    d = json.load(open(path, encoding="utf-8")) if path else fetch()
    entries = convert(d)
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump({"cves": entries}, f, ensure_ascii=False, separators=(",", ":"))
    print(f"{OUT}: {len(entries)} entries")


if __name__ == "__main__":
    main()
