```kapi-diagram type=architecture
agent: Agent (any harness) | kapi watch: one line per event | kapi say, pending, preset | writes plan.html directly | built
plan: plan.html | page source: prose + kapi-* presets | every block has a data-kapi-id | built
daemon: kapi daemon (Go, one per machine) | event log and cursors per page | page store: ids, revisions, undo | renders presets to HTML + SVG | review
disk: ~/.kapi on disk | pages/<id>/events.ndjson | cursors.json, revs/<n>.html | built
browser: Browser page | Select toggle, block toolbar | chat panel, status dot | review
hub: Private hub | R2 + Worker + Access | planned
agent -> plan: edits file
plan <-> daemon: reads, writes ops, notices your edits
agent <-> daemon: socket
browser <-> daemon: HTTP + SSE
daemon -> disk: append
daemon --> hub: publish
```

```kapi-diagram type=architecture dir=tb
a: Alpha | green
b: Beta | red
a -> b: calls
b -> b: retries
```

```
a: Alpha
a -> ghost: nobody declared it
```

```
a: Alpha
a: Alpha again
```

```
just a line
```

```
bad id!: Title
```
