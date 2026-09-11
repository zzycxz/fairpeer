import { MergeView } from '@codemirror/merge';
import { useRef, useEffect } from 'react';
import type { DiffProps } from "../DiffView";
import { EditorView, basicSetup } from 'codemirror';
import { EditorState } from '@codemirror/state';
import { oneDark } from '@uiw/react-codemirror';
import { useResolvedTheme } from "../../lib/useResolvedTheme";

export default function CodeMirrorDiff({ original, modified, maxHeight }: DiffProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  // Follow the app theme (same rule as CodeMirrorCode): the default light
  // editor for a light app theme, oneDark for dark.
  const theme = useResolvedTheme();

  useEffect(() => {
    if (!containerRef.current) return;

    // Create the merge view
    const view = new MergeView({
      a: {
        doc: original,
        extensions: [
          basicSetup,
          EditorView.editable.of(false),
          EditorState.readOnly.of(true),
          ...(theme === "dark" ? [oneDark] : [])
        ]
      },
      b: {
        doc: modified,
        extensions: [
          basicSetup,
          EditorView.editable.of(false),
          EditorState.readOnly.of(true),
          ...(theme === "dark" ? [oneDark] : [])
        ]
      },
      parent: containerRef.current
    });

    return () => {
      view.destroy();
    };
  }, [original, modified, theme]);

  return (
    <div
      className="cm-merge-wrapper"
      ref={containerRef}
      style={maxHeight ? { maxHeight, overflow: 'auto' } : undefined}
    />
  );
}
