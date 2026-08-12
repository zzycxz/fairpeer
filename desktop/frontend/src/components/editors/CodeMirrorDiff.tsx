import { MergeView } from '@codemirror/merge';
import { useRef, useEffect } from 'react';
import type { DiffProps } from "../DiffView";
import { EditorView, basicSetup } from 'codemirror';
import { EditorState } from '@codemirror/state';

export default function CodeMirrorDiff({ original, modified, maxHeight }: DiffProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  
  useEffect(() => {
    if (!containerRef.current) return;
    
    // Create the merge view
    const view = new MergeView({
      a: {
        doc: original,
        extensions: [
          basicSetup,
          EditorView.editable.of(false),
          EditorState.readOnly.of(true)
        ]
      },
      b: {
        doc: modified,
        extensions: [
          basicSetup,
          EditorView.editable.of(false),
          EditorState.readOnly.of(true)
        ]
      },
      parent: containerRef.current
    });
    
    return () => {
      view.destroy();
    };
  }, [original, modified]);

  return (
    <div 
      className="cm-merge-wrapper" 
      ref={containerRef} 
      style={maxHeight ? { maxHeight, overflow: 'auto' } : undefined} 
    />
  );
}
