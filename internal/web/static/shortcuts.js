window.kapiShortcuts = (() => {
  const mac = /Mac|iPhone|iPad/.test(navigator.platform);
  const command = mac ? "⌘" : "Ctrl+";
  const letter = (key) => ({ keys: [key], mod: false, hint: key.toUpperCase(), words: key.toUpperCase() });

  const toolbar = {
    ask: letter("a"),
    copy: letter("c"),
    archive: letter("e"),
    unarchive: letter("e"),
    expand: letter("w"),
    collapse: letter("w"),
    delete: { keys: ["Backspace", "Delete"], mod: false, hint: mac ? "⌫" : "Del", words: "Delete" },
    edit: null,
    download: null,
    "zoom-in": null,
    "zoom-out": null,
    flip: null,
  };

  const keys = {
    undo: { keys: ["z"], mod: true, hint: `${command}Z`, words: `${command}Z` },
    nextNote: letter("j"),
    previousNote: letter("k"),
    dismiss: { keys: ["Escape"], hint: "Esc", words: "Escape" },
    submit: { keys: ["Enter"], mod: true, hint: `${command}↵`, words: `${command}↵` },
    send: { keys: ["Enter"], shift: false, hint: "↵", words: "Enter" },
    newLine: { keys: ["Enter"], shift: true, hint: "Shift+↵", words: "Shift+Enter" },
    nextField: { keys: ["Tab"], shift: false, hint: "Tab", words: "Tab" },
    search: { keys: ["/"], mod: false, hint: "/", words: "/" },
    switchNote: { keys: ["."], mod: true, hint: `${command}.`, words: `${command}.`, also: { keys: ["\\"], mod: true } },
    searchPage: { keys: ["k"], mod: true, hint: `${command}K`, words: `${command}K` },
    printPage: { keys: ["p"], mod: true, hint: `${command}P`, words: `${command}P` },
    searchNext: { keys: ["ArrowDown"], hint: "↓", words: "Down arrow", also: { keys: ["n"], ctrl: true, meta: false } },
    searchPrevious: { keys: ["ArrowUp"], hint: "↑", words: "Up arrow", also: { keys: ["p"], ctrl: true, meta: false } },
    searchJump: { keys: ["Enter"], hint: "↵", words: "Enter" },
    searchCollapse: { keys: ["ArrowLeft"], hint: "←", words: "Left arrow" },
    searchExpand: { keys: ["ArrowRight"], hint: "→", words: "Right arrow" },
    menuNext: { keys: ["ArrowDown"] },
    menuPrevious: { keys: ["ArrowUp"] },
    menuFirst: { keys: ["Home"] },
    menuLast: { keys: ["End"] },
    menuOpen: { keys: ["ArrowRight", "Enter", " "] },
    menuBack: { keys: ["ArrowLeft", "Escape"] },
    menuLeave: { keys: ["Tab"] },
  };

  const matches = (binding, e) => {
    if (!binding) return false;
    if (binding.also && matches(binding.also, e)) return true;
    const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (!binding.keys.includes(key)) return false;
    if (binding.mod !== undefined && binding.mod !== (e.metaKey || e.ctrlKey)) return false;
    if (binding.ctrl !== undefined && binding.ctrl !== e.ctrlKey) return false;
    if (binding.meta !== undefined && binding.meta !== e.metaKey) return false;
    return binding.shift === undefined || binding.shift === e.shiftKey;
  };

  const known = (table, name, where) => {
    if (!(name in table)) throw new Error(`"${name}" has no entry in ${where} in static/shortcuts.js`);
    return table[name];
  };

  return Object.freeze({
    key: (name) => known(keys, name, "keys"),
    tool: (action) => known(toolbar, action, "toolbar"),
    is: (name, e) => matches(known(keys, name, "keys"), e),
    isTool: (action, e) => matches(known(toolbar, action, "toolbar"), e),
  });
})();
