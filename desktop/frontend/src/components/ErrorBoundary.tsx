import { Component, type ReactNode } from "react";
import { reportCrash } from "../lib/crash";

export class ErrorBoundary extends Component<{ children: ReactNode }, { crashed: boolean }> {
  state = { crashed: false };

  static getDerivedStateFromError() {
    return { crashed: true };
  }

  componentDidCatch(error: unknown, info: { componentStack?: string | null }) {
    reportCrash("react", error, info.componentStack ?? undefined);
  }

  render() {
    return this.state.crashed ? null : this.props.children;
  }
}

// PaneErrorBoundary — a localized boundary for major layout regions (sidebar,
// transcript, right dock, bottom bar). A crash degrades that region to a
// compact "此区域遇到错误" card instead of blanking the whole tree; the rest
// of the app keeps working (2026-08-21 — the netdev topology crash taught us
// one component's null deref shouldn't take down the composer).
export class PaneErrorBoundary extends Component<
  { children: ReactNode; label?: string },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: unknown, info: { componentStack?: string | null }) {
    reportCrash("react", error, info.componentStack ?? undefined);
  }

  render() {
    if (this.state.error) {
      return (
        <div
          style={{
            flex: "1 1 auto",
            minHeight: "60px",
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            gap: "8px",
            padding: "20px",
            color: "var(--fg-faint, #888)",
            fontSize: "12.5px",
            background: "var(--bg, #1a1a1a)",
          }}
        >
          <span>⚠ {this.props.label ?? "此区域"}遇到错误，已安全隔离</span>
          <button
            type="button"
            onClick={() => this.setState({ error: null })}
            style={{
              padding: "4px 14px",
              border: "1px solid var(--border-soft, #444)",
              borderRadius: "4px",
              background: "transparent",
              color: "inherit",
              fontSize: "11.5px",
              cursor: "pointer",
            }}
          >
            重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
