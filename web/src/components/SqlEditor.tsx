import { sql } from "@codemirror/lang-sql";
import { EditorView } from "@codemirror/view";
import CodeMirror from "@uiw/react-codemirror";

const leafyTheme = EditorView.theme({
  "&": {
    backgroundColor: "#ffffff",
    color: "#0f1f14",
    fontSize: "14px",
  },
  ".cm-content": {
    fontFamily: '"IBM Plex Mono", "SFMono-Regular", Menlo, Consolas, monospace',
    padding: "8px 0",
    lineHeight: "1.6",
  },
  ".cm-line": {
    padding: "0 12px",
  },
  ".cm-gutters": {
    backgroundColor: "#f4f7f2",
    color: "#5b6b60",
    border: "none",
    borderRight: "2px solid #0f1f14",
    minWidth: "3.5ch",
    fontFamily: '"IBM Plex Mono", "SFMono-Regular", Menlo, Consolas, monospace',
    fontSize: "14px",
  },
  ".cm-lineNumbers .cm-gutterElement": {
    padding: "0 8px",
  },
  ".cm-activeLine": { backgroundColor: "#f3faf1" },
  ".cm-activeLineGutter": {
    backgroundColor: "#e6f5e2",
    color: "#0f1f14",
    fontWeight: "600",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "#c9ecc4 !important",
  },
  ".cm-cursor": {
    borderLeftColor: "#0f1f14",
    borderLeftWidth: "2px",
  },
  "&.cm-focused": {
    outline: "none",
  },
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
    <div
      className="sqlbox focus-within:outline-3 focus-within:outline-offset-2 focus-within:outline-ink focus-within:shadow-[0_0_0_6px_#c9ecc4]"
      aria-label={ariaLabel}
    >
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
