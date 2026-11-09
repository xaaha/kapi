window.kapiTextSearch = (() => {
  const WORD = /[\p{L}\p{N}]+/gu;
  const lower = (text) => text.replace(/[\s\S]/gu, (c) => (c.toLowerCase().length === c.length ? c.toLowerCase() : c));

  function nearOne(a, b) {
    if (Math.abs(a.length - b.length) > 1) return false;
    let i = 0;
    let j = 0;
    let edits = 0;
    while (i < a.length && j < b.length) {
      if (a[i] === b[j]) {
        i++;
        j++;
        continue;
      }
      if (++edits > 1) return false;
      if (a.length >= b.length) i++;
      if (a.length <= b.length) j++;
    }
    return edits + (a.length - i) + (b.length - j) <= 1;
  }

  function compile(value) {
    const low = lower(value);
    return { terms: [...low.matchAll(WORD)].map((m) => m[0]), phrase: low.replace(/\s+/g, " ").trim() };
  }

  function index(text) {
    const low = lower(text);
    return { text, low, words: [...low.matchAll(WORD)].map((m) => [m[0], m.index]) };
  }

  function match(entry, query) {
    if (!query.terms.length) return { spans: [], score: 0 };
    let score = 0;
    const spans = [];
    for (const q of query.terms) {
      let best = 0;
      for (const [word, at] of entry.words) {
        const tier = word === q ? 3 : word.startsWith(q) ? 2 : q.length >= 5 && nearOne(q, word) ? 1 : 0;
        if (!tier) continue;
        spans.push([at, tier === 1 ? word.length : q.length]);
        best = Math.max(best, tier);
      }
      if (!best) return null;
      score += best;
    }
    const whole = query.terms.length > 1 ? entry.low.indexOf(query.phrase) : -1;
    if (whole >= 0) spans.push([whole, query.phrase.length]);
    spans.sort((a, b) => a[0] - b[0]);
    const merged = [];
    for (const [at, len] of spans) {
      const last = merged.at(-1);
      if (last && at <= last[0] + last[1]) last[1] = Math.max(last[1], at + len - last[0]);
      else merged.push([at, len]);
    }
    return { spans: merged, score: (whole >= 0 ? 100 : 0) + score };
  }

  return Object.freeze({
    compile,
    index,
    match,
    test: (text, value) => {
      const query = compile(value);
      return !query.terms.length || match(index(text), query) !== null;
    },
  });
})();
