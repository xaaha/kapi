<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.svg">
    <img src="docs/logo-light.svg" width="64" height="64" alt="" align="absmiddle">
  </picture>
  kapi
</h1>

Shared HTML pages that you and your coding agent both write to.

*Kapi* (कापी) is Nepali for notebook.

A long session with an agent buries decisions in chat. kapi gives the two of
you one page instead: the agent writes findings, plans, code and diagrams to an
HTML file, you edit, archive, reorder or ask about any block in the browser,
and every change reaches the agent as one line from `kapi wait`. It works with
any agent that can run a command, and nothing is lost if either side restarts.

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
kapi send --on cutover plan.html "is this still true?"   # as the human
kapi wait plan.html                                        # as the agent: print what is unseen and exit
kapi say --seen 4 plan.html "yes, confirmed at 14:20"      # reply as the agent, having read #4
kapi say --reply 5 plan.html "moved it to Track B"         # answer note #5, so its pin turns answered
kapi attach --after k3f9a2bq plan.html chart.png           # add an image block, stored in plan.files/
kapi export --md plan.html                                 # write plan.md, or plan.zip with its images
kapi seen plan.html 4                                      # or say you read #4 without replying
kapi status plan.html
kapi version
kapi --help
```

In Claude Code, run `kapi wait <page>` as a background command and start it
again each time it exits; its exit wakes the agent within seconds. The page
shows a message as Queued once kapi has handed it over, and 👀 only once the
agent names it with `--seen` or `kapi seen`, runs `kapi pending`, or a kapi
hook puts it in the agent's context. `kapi watch <page>`
still streams one line per event for harnesses that deliver every line.

The three-dot menu at the top of the chat panel has Export, Copy path and End
session. Export opens a submenu that saves the page as one HTML
file with its images inside, as Markdown (a zip with an images folder when the
page has images), or as a PDF through the browser's print dialog. Copy path copies the page's
path.

When you are done with a page, End session in that menu tells the agent to
stop: `kapi watch` exits, `kapi wait` wakes with the end, the Stop hook no
longer holds the agent, and `kapi` lists the page as `(ended)`. The page and
its history stay, and your next message starts the session again, marked in
the chat with a "Session started again" line.

## Agent harnesses

Run `kapi` with no arguments to see every page and what is waiting on it.
To show that to every new agent session automatically:

```sh
kapi setup hooks          # Claude Code, Codex, OpenCode and GitHub Copilot CLI
kapi setup hooks --remove # take it out again
```

This adds a SessionStart hook that runs `kapi` (Copilot CLI uses
`kapi hook-context`, which prints the same view as JSON). In Claude Code it
also adds UserPromptSubmit, PostToolUse and Stop hooks: a busy agent sees a
new message on its next tool call, and it cannot stop while a message on a
page under its directory is unseen. Re-running repairs them. Restart your
agent sessions afterwards.

## Skill

Install the kapi skill in the [Agent Skills](https://agentskills.io) format:

```sh
npx skills add xaaha/kapi --skill kapi
```

It is a short stub that sends the agent to `kapi` and `kapi --help`, so an
installed copy cannot go stale against a newer CLI.

