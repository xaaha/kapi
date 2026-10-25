```kapi-diagram
start -> parse: source
parse -> render: nodes
render -> done
parse -> error: bad input
error -> parse: retry
render -> render: nested presets
start -> done: skip everything and finish right away
a node with a rather long name that needs wrapping -> done
```

```kapi-diagram type=flow
only
```

```kapi-diagram type=flow
a --> b: dashed
# a comment line
```
