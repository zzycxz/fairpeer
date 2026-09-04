import { describe, expect, it } from "vitest";
import { detectIOCType, parseIOCList } from "../lib/ioc";

// IOC 批量导入解析（§4.6）：类型识别优先级 + 行格式（备注/注释/空行）。
describe("detectIOCType", () => {
  it("classifies IPv4 (with optional :port) and IPv6 as ip", () => {
    expect(detectIOCType("198.51.100.77")).toBe("ip");
    expect(detectIOCType("10.0.0.1:443")).toBe("ip");
    expect(detectIOCType("fe80::1")).toBe("ip");
    expect(detectIOCType("2001:db8::1")).toBe("ip");
  });
  it("classifies 32/64-hex as hash", () => {
    expect(detectIOCType("d41d8cd98f00b204e9800998ecf8427e")).toBe("hash");
    expect(detectIOCType("a".repeat(64))).toBe("hash");
  });
  it("classifies dotted alphanumeric as domain", () => {
    expect(detectIOCType("beacon.badhost.example.com")).toBe("domain");
    expect(detectIOCType("cdn-1.example.co")).toBe("domain");
  });
  it("falls back to keyword", () => {
    expect(detectIOCType("mirai")).toBe("keyword");
    expect(detectIOCType("web-01")).toBe("keyword");
  });
});

describe("parseIOCList", () => {
  it("parses one per line with auto type and # note", () => {
    const out = parseIOCList(
      "198.51.100.77 # C2 出口\nbadhost.example.com\nD41D8CD98F00B204E9800998ECF8427E # 落地样本\n/tmp/.x"
    );
    expect(out).toHaveLength(4);
    expect(out[0]).toEqual({ value: "198.51.100.77", type: "ip", note: "C2 出口" });
    expect(out[1]).toEqual({ value: "badhost.example.com", type: "domain", note: "" });
    expect(out[2].type).toBe("hash");
    expect(out[3].type).toBe("keyword");
  });
  it("skips empty lines and # comments", () => {
    expect(parseIOCList("\n\n# 头部注释\n  \n1.2.3.4\n")).toEqual([
      { value: "1.2.3.4", type: "ip", note: "" },
    ]);
  });
  it("handles CRLF and returns empty for blank input", () => {
    expect(parseIOCList("a.example.com\r\n198.51.100.1\r\n")).toHaveLength(2);
    expect(parseIOCList("   ")).toEqual([]);
  });
});
