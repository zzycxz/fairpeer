import { useCallback, useEffect, useRef, useState } from "react";

// usePanelData（SCENARIO_SPEC G1-1）：运维面板主数据的三态加载 hook——
// loading / error（文案+重试）/ empty / ok 可区分，替代先前"失败被吞、与
// 空数据不可区分"的裸 catch。错误时保留最后一次成功数据（降级展示 +
// 顶部错误条），重试即重新拉取。
//
// 用法：
//   const chain = usePanelData(() => app.NetDevInvestigationChain(sel, "", 24));
//   if (chain.status === "loading") …
//   if (chain.status === "error") return <PanelErrorState onRetry={chain.retry} />;

export interface PanelDataState<T> {
  status: "loading" | "error" | "empty" | "ok";
  data: T | null;
  error: string;
  retry: () => void;
}

export function usePanelData<T>(fetcher: () => Promise<T | null | undefined>, deps: unknown[] = []): PanelDataState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [everLoaded, setEverLoaded] = useState(false);
  const fetchRef = useRef(fetcher);
  fetchRef.current = fetcher;
  // 单调序号：deps 变化会立刻再触发一次 load，若前一次请求慢于后一次，
  // 先回的旧响应会覆盖新数据（乱序回写）。每个响应只在与最新序号一致时
  // 才落 state，过期响应直接丢弃。
  const seqRef = useRef(0);

  const load = useCallback(() => {
    const seq = ++seqRef.current;
    setError("");
    fetchRef
      .current()
      .then((v) => {
        if (seq !== seqRef.current) return; // superseded by a newer load
        setData((v as T) ?? null);
        setEverLoaded(true);
      })
      .catch((e: unknown) => {
        if (seq !== seqRef.current) return;
        // 保留 last data（如有）供降级渲染；error 态由面板显示重试。
        setError(String((e as Error)?.message ?? e));
        setEverLoaded(true);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => { load(); }, deps); // eslint-disable-line react-hooks/exhaustive-deps

  if (error) return { status: "error", data, error, retry: load };
  if (!everLoaded) return { status: "loading", data: null, error: "", retry: load };
  if (data == null) return { status: "empty", data: null, error: "", retry: load };
  return { status: "ok", data, error: "", retry: load };
}
