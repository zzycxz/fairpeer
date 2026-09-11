import CodeMirror from '@uiw/react-codemirror';
import type { EditorProps } from "../CodeViewer";
import { useResolvedTheme } from "../../lib/useResolvedTheme";

export default function CodeMirrorCode({ value, readOnly, maxHeight }: EditorProps) {
  // Follow the app theme (lib/theme.ts): light app theme renders the light
  // editor, dark renders dark. The resolved snapshot re-renders on a settings
  // flip or an OS scheme change under "auto".
  const theme = useResolvedTheme();

  return (
    <div className="cm-wrapper" style={maxHeight ? { maxHeight, overflow: 'auto' } : undefined}>
      <CodeMirror
        value={value}
        theme={theme}
        editable={!readOnly}
        readOnly={readOnly}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          dropCursor: false,
          allowMultipleSelections: false,
          indentOnInput: false
        }}
      />
    </div>
  );
}
