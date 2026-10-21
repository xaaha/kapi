import { startFakeServer } from "./fake-server.mjs";

const server = await startFakeServer();
server.push({ kind: "presence", text: "watching" });
server.push({ author: "claude", kind: "message", text: "I drafted the migration plan. The rollout form at the bottom needs your call." });
server.push({ author: "human", kind: "message", target: "bbbbbbbb", text: "Why two steps instead of one cutover?" });
server.push({ author: "claude", kind: "message", target: "bbbbbbbb", text: "Copying first lets us verify row counts before any reads move." });
console.log(server.url);
