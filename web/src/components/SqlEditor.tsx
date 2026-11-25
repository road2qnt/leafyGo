import { sql } from "@codemirror/lang-sql";
import { EditorView } from "@codemirror/view";
import CodeMirror from "@uiw/react-codemirror";

const leafyTheme = EditorView.theme({
  "&": {
    backgroundColor: "#fffef9",
    color: "#16211a",
    fontSize: "12.5px",
  },
  ".cm-content": {
    fontFamily: '"IBM Plex Mono", ui-monospace, monospace',
    padding: "12px 0",
  },
  ".cm-gutters": {
    backgroundColor: "#f4f3ec",
    color: "#8a948b",
    border: "none",
    borderRight: "1px solid #e6e5da",
  },
  ".cm-activeLine": { backgroundColor: "#f2f7f1" },
  ".cm-activeLineGutter": { backgroundColor: "#e8efe5" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "#d9f37b88",
  },
  ".cm-cursor": { borderLeftColor: "#235237" },
  "&.cm-focused": { outline: "none" },
});

export function SqlEditor({
  value,
  onChange,
  height = "260px",
  readOnly = false,
  ariaLabel = "SQL editor",
}: {
  value: string;
  onChange: (value: string) => void;
  height?: string;
  readOnly?: boolean;
  ariaLabel?: string;
}) {
  return (
    <div className="sqlbox" aria-label={ariaLabel}>
      <CodeMirror
        value={value}
        height={height}
        readOnly={readOnly}
        extensions={[sql(), EditorView.lineWrapping, leafyTheme]}
        onChange={onChange}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: !readOnly,
          highlightActiveLineGutter: !readOnly,
          autocompletion: true,
          bracketMatching: true,
          closeBrackets: true,
        }}
      />
    </div>
  );
}
