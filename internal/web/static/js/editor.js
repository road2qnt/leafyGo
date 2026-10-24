(() => {
  "use strict";

  const INDENT = "    ";
  const PAIRS = { "(": ")", "[": "]", "'": "'", '"': '"' };
  const CLOSERS = new Set([")", "]"]);
  const isMac = /Mac|iPhone|iPad/.test(navigator.platform);

  // execCommand menjaga riwayat undo bawaan; setRangeText sebagai fallback.
  function replace(area, start, end, text) {
    area.focus();
    area.setSelectionRange(start, end);
    const ok = text === "" ? document.execCommand("delete") : document.execCommand("insertText", false, text);
    if (!ok) {
      area.setRangeText(text, start, end, "end");
      area.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }

  // Simbol Mac dengan nama untuk screen reader.
  const MAC_KEYS = {
    mod: ["\u2318", "Command"],
    shift: ["\u21e7", "Shift"],
    enter: ["\u21a9", "Return"],
  };

  function shortcut(keys, action) {
    const item = document.createElement("span");
    item.className = "shortcut";
    for (const key of keys) {
      const kbd = document.createElement("kbd");
      if (Array.isArray(key)) {
        const symbol = document.createElement("span");
        symbol.setAttribute("aria-hidden", "true");
        symbol.textContent = key[0];
        const name = document.createElement("span");
        name.className = "visually-hidden";
        name.textContent = key[1];
        kbd.append(symbol, name);
      } else {
        kbd.textContent = key;
      }
      item.append(kbd, " ");
    }
    item.append(action);
    return item;
  }

  function lineStart(value, pos) {
    return value.lastIndexOf("\n", pos - 1) + 1;
  }

  function indentBlock(area, outdent) {
    const { value, selectionStart: start, selectionEnd: end } = area;
    const from = lineStart(value, start);
    // Seleksi yang berakhir tepat di awal baris tidak ikut baris tersebut.
    const to = end > start && value[end - 1] === "\n" ? end - 1 : end;
    const lines = value.slice(from, to).split("\n");
    const changed = lines.map((line) => {
      if (!outdent) return INDENT + line;
      const strip = line.startsWith("\t") ? 1 : line.match(/^ {0,4}/)[0].length;
      return line.slice(strip);
    });
    const text = changed.join("\n");
    replace(area, from, to, text);
    if (start === end && lines.length === 1) {
      const pos = Math.max(from, start + (text.length - (to - from)));
      area.setSelectionRange(pos, pos);
    } else {
      area.setSelectionRange(from, from + text.length);
    }
  }

  function newline(area) {
    const { value, selectionStart: start, selectionEnd: end } = area;
    const before = value.slice(lineStart(value, start), start);
    let indent = before.match(/^[ \t]*/)[0];
    if (/\(\s*$/.test(before)) indent += INDENT;
    replace(area, start, end, "\n" + indent);
  }

  function pair(area, key) {
    const { value, selectionStart: start, selectionEnd: end } = area;
    const next = value[start];
    if (start !== end) {
      replace(area, start, end, key + value.slice(start, end) + PAIRS[key]);
      area.setSelectionRange(start + 1, end + 1);
      return true;
    }
    if (next === key && (CLOSERS.has(key) || key === "'" || key === '"')) {
      area.setSelectionRange(start + 1, start + 1);
      return true;
    }
    if (!(key in PAIRS)) return false;
    const nextFree = next === undefined || /[\s),;\]]/.test(next);
    const prevWord = /\w/.test(value[start - 1] || "");
    if (!nextFree || ((key === "'" || key === '"') && prevWord)) return false;
    replace(area, start, end, key + PAIRS[key]);
    area.setSelectionRange(start + 1, start + 1);
    return true;
  }

  function unpair(area) {
    const { value, selectionStart: start, selectionEnd: end } = area;
    if (start !== end || start === 0) return false;
    const open = value[start - 1];
    if (!(open in PAIRS) || value[start] !== PAIRS[open]) return false;
    replace(area, start - 1, start + 1, "");
    return true;
  }

  function setup(editor) {
    const gutter = editor.querySelector(".code-editor__gutter");
    const area = editor.querySelector("textarea");
    if (!gutter || !area) return;
    const form = area.form;
    let errorLine = Number(editor.dataset.errorLine) || 0;
    let count = 0;
    let escaped = false;

    const render = (force) => {
      const next = area.value.split("\n").length;
      if (next === count && !force) return;
      count = next;
      const lines = document.createDocumentFragment();
      for (let i = 1; i <= count; i++) {
        const line = document.createElement("span");
        line.textContent = String(i);
        if (i === errorLine) line.className = "is-error";
        lines.append(line);
      }
      gutter.replaceChildren(lines);
    };

    gutter.hidden = false;
    render(true);
    if (errorLine) {
      const height = parseFloat(getComputedStyle(area).lineHeight) || 20;
      area.scrollTop = Math.max(0, (errorLine - 1) * height - area.clientHeight / 3);
    }

    area.addEventListener("input", () => {
      const stale = errorLine !== 0;
      errorLine = 0;
      render(stale);
    });
    area.addEventListener("scroll", () => {
      gutter.scrollTop = area.scrollTop;
    });

    area.addEventListener("keydown", (event) => {
      if (event.isComposing) return;
      const mod = isMac ? event.metaKey : event.ctrlKey;

      if (event.key === "Escape") {
        escaped = true;
        return;
      }
      if (event.key === "Tab" && !mod && !event.altKey) {
        if (escaped) return;
        event.preventDefault();
        const { selectionStart: start, selectionEnd: end } = area;
        if (!event.shiftKey && start === end) {
          replace(area, start, end, INDENT);
        } else {
          indentBlock(area, event.shiftKey);
        }
        return;
      }
      escaped = false;

      if (event.key === "Enter" && mod && form) {
        const button = form.querySelector(`[data-shortcut="${event.shiftKey ? "explain" : "save"}"]`);
        if (button) {
          event.preventDefault();
          form.requestSubmit(button);
        }
        return;
      }
      if (event.ctrlKey || event.metaKey || event.altKey) return;
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        newline(area);
      } else if (event.key === "Backspace") {
        if (unpair(area)) event.preventDefault();
      } else if (event.key.length === 1 && (event.key in PAIRS || CLOSERS.has(event.key))) {
        if (pair(area, event.key)) event.preventDefault();
      }
    });
    area.addEventListener("blur", () => {
      escaped = false;
    });

    const hint = editor.parentElement.querySelector("[data-editor-hint]");
    if (hint) {
      hint.replaceChildren(
        shortcut(isMac ? [MAC_KEYS.mod, MAC_KEYS.enter] : ["Ctrl", "Enter"], "to save"),
        shortcut(isMac ? [MAC_KEYS.shift, MAC_KEYS.mod, MAC_KEYS.enter] : ["Ctrl", "Shift", "Enter"], "to explain"),
      );
      hint.hidden = false;
      area.setAttribute("aria-describedby", `${area.getAttribute("aria-describedby") || ""} ${hint.id}`.trim());
    }
  }

  document.querySelectorAll("[data-code-editor]").forEach(setup);
})();
