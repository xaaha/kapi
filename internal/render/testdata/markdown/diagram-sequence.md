```kapi-diagram type=sequence
client
server
db
client -> server: GET /p/1
server -> db: SELECT
db --> server: rows
server -> server: render
server --> client: 200 OK
client -> db: never happens directly
db -> db: vacuum
```
