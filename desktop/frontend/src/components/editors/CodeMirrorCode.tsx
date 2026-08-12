import CodeMirror from '@uiw/react-codemirror';
import type { EditorProps } from "../CodeViewer";

export default function CodeMirrorCode({ value, readOnly, maxHeight }: EditorProps) {
  // Pass the system's global class standard directly,
  // typically the app determines dark/light mode and sets it in higher-level CSS.
  // By default, we let CodeMirror use its light or we could wire it to dark mode 
  // depending on `data-theme-style="graphite"` in styles.css.
  const theme = "dark"; // Defaulting to dark since it's a graphite theme

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
