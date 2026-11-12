```html
  <style>
    .cards { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px }
    .card { padding:16px; border:1px solid var(--kapi-border); border-radius:var(--kapi-radius-lg); background:var(--kapi-surface) }
  </style>
  <main><h1>Funding</h1><section class="cards"><article class="card"><h2>Current</h2><p>No card.</p></article><article class="card"><h2>Proposed</h2><button onclick="bad()">Submit a fee</button><script>bad()</script></article></section></main>
```

![Before](data:image/png;base64,AAAA)

*Before*

![After](data:image/png;base64,BBBB)

*After*
