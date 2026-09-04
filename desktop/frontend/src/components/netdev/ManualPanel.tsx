import { useState } from "react";
import ReactMarkdown from "react-markdown";
import { BookOpen } from "lucide-react";
import { useT } from "../../lib/i18n";
import usageMd from "../../guides/NETDEV_USAGE.md?raw";
import helpMd from "../../guides/NETDEV_HELP.md?raw";
import browserMd from "../../guides/browser-ops-guide.md?raw";

// ManualPanel — 应用内手册（UI_GUIDANCE_SPEC G1-2）：三份仓库指南经 ?raw 打包
// 嵌入（不新增后端桥），副本与 docs/ 原件的同步由 guides-drift 测试守护。
// 靶场安全闭环场景卡直达本页签的 usage 篇（七条主流程 §B/E 即闭环路线）。

const DOCS: { key: string; file: string }[] = [
  { key: "usage", file: usageMd },
  { key: "help", file: helpMd },
  { key: "browser", file: browserMd },
];

export function ManualPanel({ initialDoc }: { initialDoc?: string }) {
  const t = useT();
  const [doc, setDoc] = useState(initialDoc && DOCS.some(d => d.key === initialDoc) ? initialDoc : "usage");
  const current = DOCS.find(d => d.key === doc) ?? DOCS[0];
  return (
    <div className="ndv__card">
      <div className="ndv__card-title"><BookOpen size={13} style={{ verticalAlign: -2, marginRight: 4 }} />{t("ndv.man.title")}</div>
      <div className="ndv__quick-cmds" style={{ marginBottom: 8 }}>
        {DOCS.map(d => (
          <span
            key={d.key}
            className={`btn btn--small ${doc === d.key ? "btn--primary" : "btn--secondary"}`}
            role="button"
            onClick={() => setDoc(d.key)}
          >{t(`ndv.man.${d.key}` as never)}</span>
        ))}
      </div>
      <div className="ndv__manual-md">
        <ReactMarkdown>{current.file}</ReactMarkdown>
      </div>
      <div className="ndv__hint ndv__hint--flush" style={{ marginTop: 8 }}>{t("ndv.man.footer")}</div>
    </div>
  );
}
