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

## Skill

Install the kapi skill in the [Agent Skills](https://agentskills.io) format:

```sh
npx skills add xaaha/kapi --skill kapi
```

It is a short stub that sends the agent to `kapi` and `kapi --help`, so an
installed copy cannot go stale against a newer CLI.

