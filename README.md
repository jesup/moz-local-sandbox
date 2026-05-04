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
| `~/.config/claude`, `~/.local/share/claude` | ro | Claude config/data |
| `~/.config/gh`, `~/.ssh`, `~/.config/jj` | ro | VCS credentials |
| `~/.gitconfig`, `~/.arcrc`, `~/.moz-phab-config` | ro/rw | VCS config |
| `~/.nvm`, `~/.local/bin` | ro | Node, local tools |
| `~/src` | rw | Firefox source tree |
| `~/.claude`, `~/.claude.json` | rw | Claude state |
| `~/.local/share/rr` | rw | rr traces |
| `~/.local/share/uv` | rw | uv package cache |
| `~/.mozbuild` | rw | mach build artifacts |
| `~/.cargo`, `~/.rustup` | rw | Rust toolchain |
