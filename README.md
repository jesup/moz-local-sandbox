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

Three changes are required on the host before the sandbox works correctly with
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

### 3. rr pidfd_getfd patch (rr source)

`rr`'s `pidfd_getfd` retrieval path only handles `ENOSYS` as a fallback
signal; on a system returning `EPERM` (which bwrap namespacing can cause), it
asserts instead of falling back to the `sendmsg`-over-socketpair path. Build
`rr` from a patched source that treats `EPERM` the same as `ENOSYS` in that
code path.

This is an `rr` upstream issue; check whether it has been fixed before patching
locally.

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
