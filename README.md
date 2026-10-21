# kapi

Shared HTML pages that you and your coding agent both write to.

Status: phase 0. A daemon keeps a durable event log per page; `kapi watch`
streams it to the agent and survives restarts without losing or replaying
events. The browser editor comes next.

```sh
go install github.com/xaaha/kapi/cmd/kapi@latest

kapi send plan.html --on cutover "is this still true?"   # as the human
kapi watch plan.html                                       # as the agent: one line per event
kapi say plan.html "yes, confirmed at 14:20"               # reply as the agent
kapi status plan.html
kapi --help
```

In Claude Code, run `kapi watch <page>` under the Monitor tool so each event
arrives as a notification while the agent keeps working.

## Agent harnesses

Run `kapi` with no arguments to see every page and what is waiting on it.
To show that to every new agent session automatically:

```sh
kapi setup hooks          # Claude Code, Codex, OpenCode and GitHub Copilot CLI
kapi setup hooks --remove # take it out again
```

This adds a SessionStart hook that runs `kapi` (Copilot CLI uses
`kapi hook-context`, which prints the same view as JSON). Re-running repairs it.
Restart your agent sessions afterwards.

