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

## How it works

<img src="docs/architecture.svg" alt="kapi runtime architecture: an agent writes an HTML page and talks to the Go daemon over a Unix socket; the daemon serves the page and embedded JavaScript to the browser over HTTP and SSE, and stores durable state as plain files under ~/.kapi" width="100%">

Homebrew installs one Go binary. That binary contains the CLI, daemon, renderer,
agent skill, and the JavaScript, CSS, fonts and icons used by the browser. There
is no Node runtime and no separate JavaScript process: the Go daemon serves the
embedded assets on localhost, then the browser runs `kapi.js` and talks back to
Go with HTTP POSTs and server-sent events.

The page file remains in your project. Events, revisions and reader positions
are ordinary files under `$KAPI_HOME` (default `~/.kapi`); kapi does not use
SQLite or a database server.

## Install

kapi ships via [xaaha/tap](https://github.com/xaaha/homebrew-tap). Homebrew 6.0+ requires explicit trust for third-party taps; without it, `brew upgrade` silently skips kapi. One-time step per machine:

```sh
brew trust xaaha/tap
brew install --cask xaaha/tap/kapi
kapi setup
```

`kapi setup` asks which installed agents to connect, then installs or repairs
kapi's embedded skill and startup hooks. It does not use Node or `npx`.

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

Run `kapi setup` after installation. Detected agents are preselected; press
Enter to accept them or enter a comma-separated list:

```sh
kapi setup                              # ask, then install/repair
kapi setup --agents claude,codex,pi    # non-interactive selection
kapi setup --project --agents claude,pi # project-local skills
kapi setup --remove                     # remove only kapi-managed files/hooks
```

Global skills are written to the standard harness paths, including
`~/.claude/skills/kapi/SKILL.md`, `~/.agents/skills/kapi/SKILL.md` for Codex
and Pi, `~/.config/opencode/skills/kapi/SKILL.md`, and
`~/.copilot/skills/kapi/SKILL.md`. Re-running repairs them.

The skill contains stable rules and sends agents to `kapi`, `kapi --help` and
`kapi preset` for current commands and page structure, so an installed copy
does not become a stale CLI manual. Startup hooks provide ambient page and
unread-message discovery where a harness supports them. Restart agent sessions
after setup.

