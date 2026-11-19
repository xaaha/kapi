window.kapiCreateMedia = ({ main, icon, shortcuts }) => {
  const imageSelector = 'figure > img[src], [data-kapi-preset="gallery"] img[src]';
  const cellImage = ":is(td, th) img[src]";
  let current = -1;
  let pool = null;
  let before = null;
  let inerted = [];

  const button = (className, label, glyph) => {
    const el = document.createElement("button");
    el.type = "button";
    el.className = className;
    el.setAttribute("aria-label", label);
    el.append(icon(glyph));
    return el;
  };

  const scrim = document.createElement("div");
  scrim.className = "kapi-media-scrim";
  scrim.hidden = true;
  const viewer = document.createElement("div");
  viewer.className = "kapi-media-viewer";
  viewer.setAttribute("role", "dialog");
  viewer.setAttribute("aria-modal", "true");
  viewer.setAttribute("aria-label", "Image preview");
  viewer.tabIndex = -1;
  viewer.hidden = true;
  const close = button("kapi-media-close", "Close image preview", "x");
  const previous = button("kapi-media-previous", "Previous image", "chevron");
  const next = button("kapi-media-next", "Next image", "chevron");
  const picture = document.createElement("img");
  picture.className = "kapi-media-image";
  const caption = document.createElement("div");
  caption.className = "kapi-media-caption";
  viewer.append(close, previous, picture, next, caption);
  document.body.append(scrim, viewer);

  const images = () => [...document.querySelectorAll(`${imageSelector}, a.kapi-thumb > img[src]`)].filter((img) => img.getClientRects().length && !img.closest("[data-kapi-state=archived]"));
  const labelOf = (img, at, total) => {
    const text = img.closest("figure")?.querySelector("figcaption")?.textContent.trim() || img.alt.trim();
    return `${at + 1} of ${total}${text ? ` · ${text}` : ""}`;
  };
  const showing = () => (pool ? pool().filter((img) => img.isConnected && img.getClientRects().length) : images());
  const show = (at) => {
    const all = showing();
    if (!all.length) return closeViewer();
    current = (at + all.length) % all.length;
    const img = all[current];
    picture.src = img.currentSrc || img.src;
    picture.alt = img.alt;
    caption.textContent = labelOf(img, current, all.length);
    previous.hidden = next.hidden = all.length < 2;
  };
  const isOpen = () => !viewer.hidden;
  const closest = (target, selector) => target instanceof Element ? Element.prototype.closest.call(target, selector) : null;
  const focusable = () => [close, previous, next].filter((el) => !el.hidden);
  const setBackgroundInert = (inert) => {
    if (inert) {
      inerted = [...document.body.children].filter((el) => el !== viewer && el !== scrim && !el.inert);
      for (const el of inerted) el.inert = true;
      return;
    }
    for (const el of inerted) el.inert = false;
    inerted = [];
  };
  const tableOf = (img) => img.closest(cellImage.replace(" img[src]", ""))?.closest("table");
  const openImage = (img) => {
    const table = tableOf(img);
    pool = table ? () => [...table.querySelectorAll(cellImage)] : null;
    const all = showing();
    const at = all.indexOf(img);
    if (at < 0) return;
    if (!isOpen()) {
      before = document.activeElement;
      viewer.hidden = false;
      scrim.hidden = false;
      setBackgroundInert(true);
      document.documentElement.setAttribute("data-kapi-media-open", "");
    }
    show(at);
    close.focus();
  };
  function closeViewer() {
    if (!isOpen()) return;
    viewer.hidden = true;
    scrim.hidden = true;
    picture.removeAttribute("src");
    pool = null;
    document.documentElement.removeAttribute("data-kapi-media-open");
    setBackgroundInert(false);
    if (before?.isConnected) before.focus({ preventScroll: true });
    before = null;
  }
  const step = (by) => isOpen() && show(current + by);

  close.addEventListener("click", closeViewer);
  previous.addEventListener("click", () => step(-1));
  next.addEventListener("click", () => step(1));
  scrim.addEventListener("click", closeViewer);
  const editable = (img) => !!img.closest('[contenteditable=""], [contenteditable="true"]');
  const opensOnClick = (img) => main.contains(img) && !editable(img);
  document.addEventListener("keydown", (event) => {
    if (!isOpen()) {
      const img = closest(document.activeElement, cellImage);
      if (!img || !main.contains(img) || editable(img) || !shortcuts.is("activate", event)) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      openImage(img);
      return;
    }
    if (shortcuts.is("dismiss", event)) {
      closeViewer();
      event.preventDefault();
    } else if (shortcuts.is("previousImage", event)) {
      step(-1);
      event.preventDefault();
    } else if (shortcuts.is("nextImage", event)) {
      step(1);
      event.preventDefault();
    } else if (shortcuts.is("focusCycle", event)) {
      const items = focusable();
      const at = items.indexOf(document.activeElement);
      const nextAt = event.shiftKey ? (at <= 0 ? items.length - 1 : at - 1) : (at < 0 || at === items.length - 1 ? 0 : at + 1);
      event.preventDefault();
      items[nextAt].focus();
    }
    event.stopImmediatePropagation();
  }, true);
  document.addEventListener("click", (event) => {
    const thumb = closest(event.target, cellImage);
    if (thumb && opensOnClick(thumb)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      openImage(thumb);
      return;
    }
    const link = closest(event.target, "a.kapi-thumb");
    if (!link) return;
    const img = link.querySelector("img[src]");
    if (!img) return;
    event.preventDefault();
    openImage(img);
  }, true);
  document.addEventListener("dblclick", (event) => {
    const img = closest(event.target, `${imageSelector}, ${cellImage}`);
    if (!img || !main.contains(img) || editable(img)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    openImage(img);
  }, true);

  let paletteCSS;
  function paletteRules() {
    if (paletteCSS !== undefined) return paletteCSS;
    const rules = [];
    for (const sheet of document.styleSheets) {
      let list;
      try {
        list = sheet.cssRules;
      } catch {
        continue;
      }
      for (const rule of list) if (rule.selectorText?.includes("[data-palette=")) rules.push(rule.cssText);
    }
    const css = rules.join("\n");
    if (css) paletteCSS = css;
    return css;
  }

  function fitFrame(frame) {
    try {
      const doc = frame.contentDocument;
      if (!doc) return;
      const fit = () => {
        const height = Math.max(320, Math.min(960, doc.documentElement.scrollHeight));
        frame.style.height = `${height}px`;
      };
      fit();
      new ResizeObserver(fit).observe(doc.documentElement);
      const palettes = doc.createElement("style");
      palettes.textContent = paletteRules();
      doc.head.append(palettes);
      const syncTheme = () => {
        for (const key of ["theme", "palette"]) {
          const value = document.documentElement.dataset[key];
          if (value) doc.documentElement.dataset[key] = value;
          else delete doc.documentElement.dataset[key];
        }
      };
      syncTheme();
      document.addEventListener("kapi-theme", syncTheme);
      document.addEventListener("kapi-palette", syncTheme);
    } catch {}
  }

  function prepare(scope = main) {
    const galleryImages = scope.matches?.(imageSelector) ? [scope] : [...scope.querySelectorAll(imageSelector)];
    for (const img of galleryImages) {
      if (!img.title && !img.matches(cellImage)) img.title = "Double-click to open · P";
    }
    const thumbs = scope.matches?.(cellImage) ? [scope] : [...scope.querySelectorAll(cellImage)];
    for (const img of thumbs) {
      if (img.hasAttribute("data-kapi-thumb")) continue;
      const added = ["tabindex", "role", "aria-label", "title"].filter((name) => !img.hasAttribute(name));
      img.setAttribute("data-kapi-thumb", added.join(" "));
      if (added.includes("tabindex")) img.tabIndex = 0;
      if (added.includes("role")) img.setAttribute("role", "button");
      if (added.includes("aria-label")) img.setAttribute("aria-label", img.alt.trim() ? `Open image: ${img.alt.trim()}` : "Open image");
      if (added.includes("title")) img.title = "Click to open";
    }
    const frames = scope.matches?.("iframe.kapi-render-frame") ? [scope] : [...scope.querySelectorAll("iframe.kapi-render-frame")];
    for (const frame of frames) {
      if (frame.dataset.kapiMediaReady) continue;
      frame.dataset.kapiMediaReady = "";
      frame.addEventListener("load", () => fitFrame(frame));
      if (frame.contentDocument?.readyState === "complete") fitFrame(frame);
    }
  }

  function clean(node) {
    for (const img of node.querySelectorAll("img[data-kapi-thumb]")) {
      for (const name of img.getAttribute("data-kapi-thumb").split(" ").filter(Boolean)) img.removeAttribute(name);
      img.removeAttribute("data-kapi-thumb");
    }
    return node;
  }

  return Object.freeze({
    clean,
    has: (block) => !!block?.querySelector("img[src]"),
    open: (block) => {
      const img = block?.matches("img[src]") ? block : block?.querySelector("img[src]");
      if (img) openImage(img);
    },
    prepare,
  });
};
