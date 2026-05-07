# moz-local-sandbox

`bwrap`-based sandbox for running Claude Code (`claude`) against a Firefox
checkout with `rr` recording/replay support via the rr-mcp MCP server.

## Usage

```
ccode [claude-args...]
```

The script launches `claude --permission-mode bypassPermissions` inside a
`bwrap` container. `~/src` and state directories are writable; most of the
system is read-only. Network is shared (needed for `mach`).

Install `ccode` somewhere on your `$PATH` (e.g. `~/bin/ccode`) and `chmod +x`
it.

### Choosing what to expose read-write

By default the sandbox bind-mounts `~/src` rw, so the agent can hop between
checkouts. Two env vars let you tighten or relocate this:

- `CCODE_SRC=/path/to/tree` — use a different root than `~/src`.
- `CCODE_CWD_ONLY=1` — expose **only** the current working directory rw,
  nothing else under `~/src`. Smaller blast radius if the agent goes off the
  rails; the cost is no cross-repo work in that session.

## Host system setup

Two changes are required on the host before the sandbox works correctly with
Firefox+rr.

### 1. AppArmor profile

Replace `/etc/apparmor.d/bwrap-userns-restrict` with the file in
`apparmor/bwrap-userns-restrict`, then reload:

```
sudo cp apparmor/bwrap-userns-restrict /etc/apparmor.d/bwrap-userns-restrict
sudo apparmor_parser -r /etc/apparmor.d/bwrap-userns-restrict
```

**Why:** The stock `unpriv_bwrap` profile contains `audit deny capability`,
which blocks `perf_event_open` capability checks when Firefox spawns child
processes that create their own user namespaces. AppArmor enforces these checks
across namespace boundaries, causing `rr` to fail. The patched profile has that
line commented out.

### 2. perf_event_paranoid sysctl

```
sudo cp sysctl/10-perf.conf /etc/sysctl.d/10-perf.conf
sudo sysctl -p /etc/sysctl.d/10-perf.conf
```

**Why:** `rr` requires `perf_event_open`. The default paranoia level on Ubuntu
(≥3) blocks this for unprivileged processes. Setting it to 1 allows it.

### 3. Disable per-repo git hooks on the host (recommended)

The sandbox can write to any repository under `~/src`, including its
`.git/hooks/` and `.git/config`. Inside the sandbox we disable hook execution,
but a compromised agent can still drop a `.git/hooks/post-merge` (or set
`core.hooksPath` / `core.fsmonitor` / a `[alias] x = !cmd` in `.git/config`)
that the *host's* git would later execute as you, outside the sandbox.

The cleanest defence is to make your host git ignore per-repo hooks entirely:

```
git config --global core.hooksPath ~/.git-hooks-trusted
mkdir -p ~/.git-hooks-trusted
```

Per-repo `.git/config` is harder to neutralise — treat sandbox-touched repos
as untrusted on the host, and audit `.git/config` before running git commands
in them if you suspect compromise.

## What the sandbox exposes

| Path | Access | Purpose |
|------|--------|---------|
| `/usr`, `/lib`, `/lib64`, `/bin` | ro | system binaries/libs |
| `/etc/{resolv.conf,hosts,ssl,passwd,group}` | ro | network + auth |
| `/etc/alternatives/cc` | ro | default C compiler symlink |
| `~/.config/claude`, `~/.local/share/claude` | ro | Claude config/data |
| `~/.config/gh`, `~/.config/jj` | ro | VCS credentials |
| `~/.gitconfig`, `~/.arcrc`, `~/.moz-phab-config` | ro/rw | VCS config |
| `~/.nvm`, `~/.local/bin` | ro | Node, local tools |
| `~/.rustup` | ro | rust toolchains (use only) |
| `~/.cargo/bin` → `/opt/cargo-host/bin` | ro | host-installed cargo tools (jj, bat, …) |
| `~/.ssh/{known_hosts,config}` | ro | ssh known hosts / config (keys NOT exposed; see below) |
| `$SSH_AUTH_SOCK` | ro | ssh-agent socket forwarded for signing |
| `~/src` (or `$CCODE_SRC`, or `$PWD` with `CCODE_CWD_ONLY=1`) | rw | Firefox source tree |
| `~/.claude`, `~/.claude.json` | rw | Claude state |
| `~/.local/share/rr` | rw | rr traces |
| `~/.mozbuild` | rw | mach build artifacts |
| `~/.sandbox/{cargo,uv,npm,npm-prefix,pip,go}` (mounted at canonical paths) | rw | sandbox-only language toolchain caches; `npm-prefix/bin` is on `PATH` for `npm i -g` |

The following environment variables are forwarded into the sandbox when
present: `GH_TOKEN` (read at launch via `gh auth token`), `PHABRICATOR_TOKEN`,
`BMO_API_KEY`, and `SSH_AUTH_SOCK`. Everything else from the host environment
is dropped via `--clearenv`.

## Residual risks

The sandbox limits blast radius but does not eliminate it. Things to be aware
of:

- **Per-repo `.git/config`.** The agent can edit `.git/config` in any repo
  under `~/src`. Settings like `core.hooksPath`, `core.fsmonitor` or
  `[alias] x = !shell-cmd` will be honoured by the *host's* git. Mitigate by
  setting `core.hooksPath` in your own `~/.gitconfig` (see Host system
  setup) and by treating sandbox-touched repos as untrusted on the host.

- **Bearer tokens are readable, not just unmodifiable.** `~/.arcrc`,
  `~/.config/gh` and `~/.moz-phab-config` are exposed so the agent can use
  Phabricator / GitHub. Read-only mounts stop tampering but a compromised
  agent can still copy the tokens out over the (shared) network. Network
  egress filtering is expected to be handled separately.

- **Claude state is shared with host claude.** `~/.claude` and
  `~/.claude.json` are writable and are the same paths the host's `claude`
  binary uses. A compromised sandbox can edit memory files, settings,
  hooks, or MCP server lists — and a subsequent host `claude` run will pick
  them up, *outside* the sandbox. If this matters, run host claude with
  separate `CLAUDE_CONFIG_DIR` / data dir.

- **rustup `~/.rustup` is shared read-only.** A compromised agent cannot
  modify the host toolchain, but cannot install new toolchains either —
  `rustup install/update` must run on the host.

- **`cargo install` no longer reaches the host.** Cargo-installed CLI tools
  live in the sandbox's `~/.cargo/bin` (under `~/.sandbox/cargo/bin`). If
  you want a tool inside the sandbox, install it from inside `ccode`.
