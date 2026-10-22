<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.svg">
    <img src="docs/logo-light.svg" width="64" height="64" alt="" align="absmiddle">
  </picture>
  kapi
</h1>

Shared HTML pages that you and your coding agent both write to.

## Install

kapi ships via [xaaha/tap](https://github.com/xaaha/homebrew-tap). Homebrew 6.0+ requires explicit trust for third-party taps; without it, `brew upgrade` silently skips kapi. One-time step per machine:

```sh
brew trust xaaha/tap
brew install --cask xaaha/tap/kapi
```

Other install options:

- `go install github.com/xaaha/kapi/cmd/kapi@latest`
- Build from source with `go build -o kapi ./cmd/kapi`

kapi runs on macOS and Linux.

### Shell completion (go install / source builds)

Homebrew installs completion automatically. If you installed via `go install`
or built from source, opt in once:

```sh
# zsh: add to ~/.zshrc, after compinit
source <(kapi completion zsh)

# bash: add to ~/.bashrc
eval "$(kapi completion bash)"
```

## Usage

Status: phase 0. A daemon keeps a durable event log per page; `kapi watch`
streams it to the agent and survives restarts without losing or replaying
events. The browser editor comes next.

```sh
kapi send plan.html --on cutover "is this still true?"   # as the human
kapi watch plan.html                                       # as the agent: one line per event
kapi say plan.html "yes, confirmed at 14:20"               # reply as the agent
kapi status plan.html
kapi version
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

## Skill

Install the kapi skill in the [Agent Skills](https://agentskills.io) format:

```sh
npx skills add xaaha/kapi --skill kapi
```

It is a short stub that sends the agent to `kapi` and `kapi --help`, so an
installed copy cannot go stale against a newer CLI.

