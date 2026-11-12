window.kapiCreateTables = ({ main, siteHead, idOf, rightEdge }) => {
  const selector = '[data-kapi-preset="table"], table[data-kapi-id]';
  const SHORT_TEXT = 24;
  const BREAKOUT_GUTTER = 24;
  const cardMode = matchMedia("(max-width: 640px)");
  const unsortedRow = new WeakMap();
  const expandedKey = `kapi-expanded-tables:${location.pathname}`;
  const expanded = new Set((() => {
    try {
      return JSON.parse(localStorage.getItem(expandedKey) || "[]");
    } catch {
      return [];
    }
  })());

  const closest = (el, query) => el && Element.prototype.closest.call(el, query);
  const matches = (el) => !!el?.matches(selector);
  const tableBox = (table) => (table.parentElement.matches(".kapi-table") ? table.parentElement : table);
  const canWiden = (box) => box.matches(".kapi-table, table[data-kapi-id]") && !cardMode.matches;
  const rowsOf = (table) => [...table.querySelectorAll("tr")].filter((tr) => closest(tr, "table") === table);
  const headRow = (table) => table.tHead?.rows[0] ?? rowsOf(table).find((tr) => tr.cells.length && [...tr.cells].every((c) => c.tagName === "TH"));
  const isHeadRow = (tr) => {
    const table = closest(tr, "table");
    return tr.parentElement === table.tHead || tr === headRow(table);
  };

  function normalizeTables(scope) {
    const nested = scope.querySelectorAll ? [...scope.querySelectorAll("table[data-kapi-id]")] : [];
    const tables = [scope.matches?.("table[data-kapi-id]") ? scope : null, ...nested].filter(Boolean);
    for (const table of tables) {
      if (table.parentElement?.matches(".kapi-table")) continue;
      const wrapper = document.createElement("div");
      wrapper.className = "kapi-table";
      wrapper.dataset.kapiPreset = "table";
      for (const name of ["data-kapi-id", "data-kapi-state", "data-kapi-archived-with"]) {
        if (!table.hasAttribute(name)) continue;
        wrapper.setAttribute(name, table.getAttribute(name));
        table.removeAttribute(name);
      }
      table.replaceWith(wrapper);
      wrapper.append(table);
    }
  }

  function labelCells(scope) {
    const tables = scope.matches?.("table") ? [scope] : [...scope.querySelectorAll('[data-kapi-preset="table"] > table')];
    for (const table of tables) {
      const rows = rowsOf(table);
      const head = headRow(table);
      if (!head) continue;
      const labels = [...head.cells].flatMap((cell) => Array(cell.colSpan).fill(cell.textContent.trim()));
      const carry = [];
      for (const tr of rows) {
        if (tr === head || tr.parentElement === table.tHead) continue;
        let col = 0;
        const skip = () => {
          while (carry[col] > 0) carry[col++]--;
        };
        for (const cell of tr.cells) {
          skip();
          cell.setAttribute("data-kapi-label", labels[col] ?? "");
          cell.setAttribute("data-kapi-col", col);
          for (let i = 0; i < cell.colSpan; i++) carry[col++] = Math.max(cell.rowSpan, 1) - 1;
        }
        skip();
      }
    }
  }

  function fitColumns(table) {
    const longest = [];
    for (const tr of rowsOf(table)) {
      if (isHeadRow(tr)) continue;
      for (const cell of tr.cells) {
        if (cell.colSpan === 1) longest[cell.cellIndex] = Math.max(longest[cell.cellIndex] ?? 0, cell.textContent.trim().length);
      }
    }
    for (const tr of rowsOf(table)) {
      for (const cell of tr.cells) {
        const fit = cell.colSpan === 1 && ((longest[cell.cellIndex] ?? 0) > SHORT_TEXT ? "text" : "short");
        if (!fit) cell.removeAttribute("data-kapi-fit");
        else if (cell.getAttribute("data-kapi-fit") !== fit) cell.setAttribute("data-kapi-fit", fit);
      }
    }
  }

  function leastWidth(table) {
    table.style.width = "min-content";
    const width = table.getBoundingClientRect().width;
    table.style.removeProperty("width");
    return width;
  }

  const breakoutBounds = () => [BREAKOUT_GUTTER, rightEdge() - BREAKOUT_GUTTER];

  function hideLogo() {
    const logo = siteHead.getBoundingClientRect();
    const overlaps = (box) => {
      const r = box.getBoundingClientRect();
      return r.left < logo.right && logo.left < r.right && r.top < logo.bottom && logo.top < r.bottom;
    };
    const covered = getComputedStyle(siteHead).position === "fixed" && [...main.querySelectorAll("table")].some((table) => tableBox(table).style.width && overlaps(tableBox(table)));
    siteHead.toggleAttribute("data-covered", covered);
  }

  function stickHeaders() {
    for (const table of main.querySelectorAll(".kapi-table > table")) {
      const head = table.tHead;
      const box = table.parentElement;
      if (!head || !box.hasAttribute("data-kapi-wide")) {
        head?.style.removeProperty("transform");
        continue;
      }
      head.style.removeProperty("transform");
      const outer = box.getBoundingClientRect();
      const at = head.getBoundingClientRect();
      const shift = Math.max(0, Math.min(-at.top, outer.bottom - at.bottom));
      if (shift) head.style.transform = `translateY(${shift}px)`;
    }
  }

  function layout() {
    const [lo, hi] = breakoutBounds();
    for (const table of main.querySelectorAll("table")) {
      fitColumns(table);
      const box = tableBox(table);
      box.style.removeProperty("width");
      box.style.removeProperty("margin-left");
      table.removeAttribute("data-kapi-tight");
      if (canWiden(box)) {
        const column = box.getBoundingClientRect().width;
        const width = expanded.has(idOf(box)) ? Math.max(hi - lo, column) : column;
        if (leastWidth(table) > width + 1) table.setAttribute("data-kapi-tight", "");
        if (width > column + 1) {
          const left = box.getBoundingClientRect().left;
          box.style.width = `${width}px`;
          box.style.marginLeft = `${Math.min(Math.max(left - (width - column) / 2, lo), hi - width) - left}px`;
        }
      }
      if (box !== table) box.toggleAttribute("data-kapi-wide", box.scrollWidth > box.clientWidth + 1);
    }
    hideLogo();
    stickHeaders();
  }

  function setExpanded(block, on) {
    if (on) expanded.add(idOf(block));
    else expanded.delete(idOf(block));
    try {
      localStorage.setItem(expandedKey, JSON.stringify([...expanded]));
    } catch {}
    layout();
  }

  function widthAction(block) {
    if (!matches(block) || !canWiden(block)) return null;
    if (block.style.width) return "collapse";
    const [lo, hi] = breakoutBounds();
    return hi - lo > block.getBoundingClientRect().width + 1 ? "expand" : null;
  }

  function pickedCell(block, point) {
    return point?.el === block && point.cell?.isConnected && block.contains(point.cell) ? point.cell : null;
  }

  function pickedRow(block, point) {
    const tr = pickedCell(block, point)?.parentElement;
    return tr && !isHeadRow(tr) ? tr : null;
  }

  function pickedColumn(block, point) {
    const cell = pickedCell(block, point);
    if (!cell) return null;
    return [...cell.parentElement.cells].slice(0, cell.cellIndex).reduce((n, c) => n + c.colSpan, 0);
  }

  function scope(block, point) {
    const cell = matches(block) && pickedCell(block, point);
    if (!cell) return "block";
    return isHeadRow(cell.parentElement) ? "column" : "row";
  }

  function sourceRow(tr) {
    const at = tr.cells[0]?.dataset.kapiCell;
    return at ? Number(at.split(",")[0]) : (unsortedRow.get(tr) ?? rowsOf(closest(tr, "table")).indexOf(tr));
  }

  function cellPart(cell) {
    if (isHeadRow(cell.parentElement)) return `column ${cell.textContent.trim()}`;
    const rows = rowsOf(closest(cell, "table")).filter((tr) => !isHeadRow(tr));
    const row = `row ${rows.indexOf(cell.parentElement) + 1}`;
    const label = cell.getAttribute("data-kapi-label");
    return label ? `${row}, ${label}` : row;
  }

  function markPicked(block, point, editing) {
    for (const el of main.querySelectorAll("[data-kapi-picked]")) el.removeAttribute("data-kapi-picked");
    if (editing) return;
    const at = scope(block, point);
    if (at === "row") return pickedRow(block, point).setAttribute("data-kapi-picked", "");
    if (at !== "column") return;
    const col = pickedColumn(block, point);
    for (const tr of rowsOf(closest(pickedCell(block, point), "table"))) {
      let index = 0;
      for (const cell of tr.cells) {
        if (index === col) cell.setAttribute("data-kapi-picked", "");
        index += cell.colSpan;
      }
    }
  }

  function sort(th) {
    const table = closest(th, "table");
    const body = table.tBodies[0];
    if (!body) return;
    const col = th.cellIndex;
    const dir = th.getAttribute("aria-sort") === "ascending" ? "descending" : "ascending";
    for (const other of th.parentElement.cells) other.removeAttribute("aria-sort");
    th.setAttribute("aria-sort", dir);
    const all = rowsOf(table);
    if (!all.some((tr) => unsortedRow.has(tr))) all.forEach((tr, i) => unsortedRow.set(tr, i));
    const rows = [...body.rows].filter((row) => row !== th.parentElement);
    const text = (row) => (row.cells[col]?.textContent || "").trim();
    const num = (value) => Number(value.replace(/,/g, ""));
    const numeric = rows.every((row) => text(row) !== "" && Number.isFinite(num(text(row))));
    const compare = numeric ? (a, b) => num(text(a)) - num(text(b)) : (a, b) => text(a).localeCompare(text(b));
    rows.sort((a, b) => (dir === "ascending" ? compare(a, b) : compare(b, a)));
    body.append(...rows);
  }

  function tappedLabel(event) {
    const cell = closest(event.target, ".kapi-table td, .kapi-table th");
    if (!cell?.hasAttribute("data-kapi-col") || getComputedStyle(cell).display !== "block") return null;
    if (event.clientX - cell.getBoundingClientRect().left >= parseFloat(getComputedStyle(cell).paddingLeft)) return null;
    const head = headRow(closest(cell, "table"));
    let col = 0;
    for (const th of head?.cells ?? []) {
      col += th.colSpan;
      if (col > Number(cell.dataset.kapiCol)) return th;
    }
    return null;
  }

  function handleClick(event, block, point) {
    const label = tappedLabel(event);
    const th = closest(event.target, ".kapi-table th");
    if (label && main.contains(label)) {
      sort(label);
      return true;
    }
    if (th && main.contains(th) && block && pickedCell(block, point) === th) {
      sort(th);
      return true;
    }
    return false;
  }

  const radius = (block) => {
    const table = block.matches("table") ? block : block.querySelector("table");
    return parseFloat(getComputedStyle(table ?? block).borderTopLeftRadius) || 0;
  };

  function prepare(scope = main) {
    normalizeTables(scope);
    labelCells(main);
    tableSizes.disconnect();
    for (const table of main.querySelectorAll(".kapi-table > table")) tableSizes.observe(table);
    layout();
  }

  const tableSizes = new ResizeObserver(layout);
  let bodyWidth = 0;

  function start() {
    addEventListener("scroll", hideLogo, { passive: true });
    addEventListener("scroll", stickHeaders, { passive: true });
    new ResizeObserver(layout).observe(main);
    new ResizeObserver(() => {
      if (document.body.clientWidth === bodyWidth) return;
      bodyWidth = document.body.clientWidth;
      layout();
    }).observe(document.body);
    cardMode.addEventListener("change", layout);
    document.fonts.ready.then(layout);
    prepare(main);
  }

  return Object.freeze({
    selector,
    matches,
    rowsOf,
    headRow,
    isHeadRow,
    prepare,
    layout,
    setExpanded,
    widthAction,
    pickedCell,
    pickedRow,
    pickedColumn,
    sourceRow,
    cellPart,
    scope,
    markPicked,
    handleClick,
    radius,
    start,
  });
};
