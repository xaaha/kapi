window.kapiCreateMedia = ({ main }) => {
  const imageSelector = 'figure > img[src], [data-kapi-preset="gallery"] img[src]';
  let current = -1;

  const svg = (path) => {
    const el = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    el.setAttribute("viewBox", "0 0 256 256");
    el.setAttribute("aria-hidden", "true");
    const p = document.createElementNS(el.namespaceURI, "path");
    p.setAttribute("d", path);
    el.append(p);
    return el;
  };
  const button = (className, label, path) => {
    const el = document.createElement("button");
    el.type = "button";
    el.className = className;
    el.setAttribute("aria-label", label);
    el.append(svg(path));
    return el;
  };

  const paths = {
    close: "M205.66,194.34a8,8,0,0,1-11.32,11.32L128,139.31,61.66,205.66a8,8,0,0,1-11.32-11.32L116.69,128,50.34,61.66A8,8,0,0,1,61.66,50.34L128,116.69l66.34-66.35a8,8,0,0,1,11.32,11.32L139.31,128Z",
    chevron: "M181.66,133.66l-80,80a8,8,0,0,1-11.32-11.32L164.69,128,90.34,53.66a8,8,0,0,1,11.32-11.32l80,80A8,8,0,0,1,181.66,133.66Z",
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
  const close = button("kapi-media-close", "Close image preview", paths.close);
  const previous = button("kapi-media-previous", "Previous image", paths.chevron);
  const next = button("kapi-media-next", "Next image", paths.chevron);
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
  const show = (at) => {
    const all = images();
    if (!all.length) return closeViewer();
    current = (at + all.length) % all.length;
    const img = all[current];
    picture.src = img.currentSrc || img.src;
    picture.alt = img.alt;
    caption.textContent = labelOf(img, current, all.length);
    previous.hidden = next.hidden = all.length < 2;
  };
  const isOpen = () => !viewer.hidden;
  const openImage = (img) => {
    const all = images();
    const at = all.indexOf(img);
    if (at < 0) return;
    viewer.hidden = false;
    scrim.hidden = false;
    document.documentElement.setAttribute("data-kapi-media-open", "");
    show(at);
    close.focus();
  };
  function closeViewer() {
    if (!isOpen()) return;
    viewer.hidden = true;
    scrim.hidden = true;
    picture.removeAttribute("src");
    document.documentElement.removeAttribute("data-kapi-media-open");
  }
  const step = (by) => isOpen() && show(current + by);

  close.addEventListener("click", closeViewer);
  previous.addEventListener("click", () => step(-1));
  next.addEventListener("click", () => step(1));
  scrim.addEventListener("click", closeViewer);
  document.addEventListener("keydown", (event) => {
    if (!isOpen()) return;
    if (event.key === "Escape") closeViewer();
    else if (event.key === "ArrowLeft") step(-1);
    else if (event.key === "ArrowRight") step(1);
    else return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);
  document.addEventListener("click", (event) => {
    const link = event.target.closest("a.kapi-thumb");
    if (!link) return;
    const img = link.querySelector("img[src]");
    if (!img) return;
    event.preventDefault();
    openImage(img);
  }, true);
  document.addEventListener("dblclick", (event) => {
    const img = event.target.closest(imageSelector);
    if (!img || !main.contains(img)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    openImage(img);
  }, true);

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
      const syncTheme = () => {
        const theme = document.documentElement.dataset.theme;
        if (theme) doc.documentElement.dataset.theme = theme;
        else doc.documentElement.removeAttribute("data-theme");
      };
      syncTheme();
      document.addEventListener("kapi-theme", syncTheme);
    } catch {}
  }

  function prepare(scope = main) {
    const galleryImages = scope.matches?.(imageSelector) ? [scope] : [...scope.querySelectorAll(imageSelector)];
    for (const img of galleryImages) {
      if (!img.title) img.title = "Double-click to open · P";
    }
    const frames = scope.matches?.("iframe.kapi-render-frame") ? [scope] : [...scope.querySelectorAll("iframe.kapi-render-frame")];
    for (const frame of frames) {
      if (frame.dataset.kapiMediaReady) continue;
      frame.dataset.kapiMediaReady = "";
      frame.addEventListener("load", () => fitFrame(frame));
      if (frame.contentDocument?.readyState === "complete") fitFrame(frame);
    }
  }

  return Object.freeze({
    has: (block) => !!block?.querySelector("img[src]"),
    open: (block) => {
      const img = block?.matches("img[src]") ? block : block?.querySelector("img[src]");
      if (img) openImage(img);
    },
    prepare,
  });
};
