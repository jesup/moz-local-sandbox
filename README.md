# moz-local-sandbox

Local sandbox for running Claude Code (`claude`) against a Firefox checkout.

- **Linux:** `bwrap`-based, supports `rr` recording/replay via the rr-mcp MCP
  server. Script: `ccode`.
- **macOS:** `sandbox-exec` (Seatbelt) based. Script: `ccode-macos`. `rr` is
  Linux-only and is not available here.

## Usage

```
ccode [claude-args...]
```

The script launches `claude --permission-mode bypassPermissions` inside a
container. `~/src` and state directories are writable; most of the system is
read-only. Network is shared (needed for `mach`).

`./install.sh` symlinks the OS-appropriate script to `~/bin/ccode`. The
macOS path also needs the network proxy binary built once: `make build`.
To install manually, copy or symlink `ccode` (Linux) or `ccode-macos`
(macOS) somewhere on your `$PATH` and `chmod +x` it.

### Choosing what to expose read-write

By default the sandbox bind-mounts `~/src` rw, so the agent can hop between
checkouts. Two env vars let you tighten or relocate this:

- `CCODE_SRC=/path/to/tree` — use a different root than `~/src`.
- `CCODE_CWD_ONLY=1` — expose **only** the current working directory rw,
  nothing else under `~/src`. Smaller blast radius if the agent goes off the
  rails; the cost is no cross-repo work in that session.

### Network filtering (macOS, opt-in)

By default the sandbox shares the host's network namespace and can reach
anywhere. `CCODE_NETPOLICY=<name>` switches it into a strict-egress mode:

- The Seatbelt profile becomes `(deny network*)` plus an allow rule for
  `localhost:<HTTP_PORT>` and `localhost:<SOCKS_PORT>` only. Direct
  connections to anywhere else die at the kernel.
- A host-side proxy binary (`bin/ccode-netproxy`, built from `netproxy/`)
  starts on those two loopback ports: an HTTP CONNECT proxy and a SOCKS5
  proxy, both filtering by hostname against the named policy's allowlist
  (with `*.example.com` wildcards).
- `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY=socks5h://…` / `NO_PROXY` are
  injected into the sandbox env so curl, git, npm, pip, cargo, Node, and
  claude itself all route through the proxy without configuration.

```
ccode                              # open network (historical default)
CCODE_NETPOLICY=anthropic-only ccode
CCODE_NETPOLICY=anthropic-mozilla ccode
CCODE_NETPOLICY=/abs/path/to/policy.json ccode
```

Bare names resolve to `policies/<name>.json` inside the repo. Existing
policies:

| Name                | Reach                                             |
|---------------------|---------------------------------------------------|
| `anthropic-only`    | Only `*.anthropic.com`. No git, no npm, no nothing else. |
| `anthropic-mozilla` | Anthropic + Mozilla services + GitHub + npm + crates.io + PyPI |

Policy file format:

```json
{
  "name": "my-policy",
  "description": "free-form, displayed nowhere yet",
  "allowedDomains": ["api.anthropic.com", "*.github.com"],
  "deniedDomains": ["evil.github.com"]
}
```

`deniedDomains` is checked first (deny wins). Wildcards (`*.foo.com`)
match subdomains only — the bare apex `foo.com` needs its own entry.

The proxy does **not** terminate TLS — filtering is at SNI/host-header
granularity, not at content level. A future iteration may add MITM
content filtering (per-method allowlists for specific APIs, request
inspection, etc.) layered on the same proxy.

### Host-side noexec (macOS, opt-in)

`CCODE_NOEXEC=1 ccode` arms a post-exit `chmod a-x` sweep over the
writable tree: any file that gained the execute bit during the sandbox
session has it stripped when the sandbox exits. Files that were already
executable before launch are left alone (mtime-based diff).

This is the macOS equivalent of sandtool's `MS_NOEXEC` bind-mount trick.
On Linux, a separate mount-namespace lets the host see the workdir as
non-executable while the sandbox sees it executable; on macOS, with no
mount namespaces, the only option is to detect-and-strip on exit.

Restore with `chmod +x <file>` on the host. There is no automatic restore.

## Host system setup

### Linux

Two changes are required on the host before the sandbox works correctly with
Firefox+rr.

#### 1. AppArmor profile

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

#### 2. perf_event_paranoid sysctl

```
sudo cp sysctl/10-perf.conf /etc/sysctl.d/10-perf.conf
sudo sysctl -p /etc/sysctl.d/10-perf.conf
```

**Why:** `rr` requires `perf_event_open`. The default paranoia level on Ubuntu
(≥3) blocks this for unprivileged processes. Setting it to 1 allows it.

#### 3. Disable per-repo git hooks on the host (recommended)

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

### macOS

No host-side changes are required: `sandbox-exec` ships in the base system.
Disabling per-repo git hooks on the host (the same recommendation as on
Linux) is still worth doing.

To verify the sandbox is enforcing the expected policy on this machine:

```
./test/test-macos.sh
```

The suite probes the live profile with read/write/exec scenarios that
should succeed, ones that should be denied, and confirms the script's
`env -i` strips host secrets while redirecting toolchain caches into
`~/.sandbox/`. It does not require `claude` to be installed.

## What the sandbox exposes

### Linux (`bwrap`)

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

### macOS (`sandbox-exec`)

| Path | Access | Purpose |
|------|--------|---------|
| `/usr`, `/bin`, `/sbin`, `/System`, `/Library`, `/Applications`, `/opt` | ro | system binaries / libs / frameworks |
| `/private/etc`, `/private/var/db` | ro | system config, dyld cache |
| `/dev`, `/private/tmp`, `/private/var/folders` | rw | devices, tmp, per-user temp dirs |
| `~/.config/claude`, `~/.local/share/claude` | ro | Claude config/data |
| `~/.config/gh`, `~/.config/jj` | ro | VCS credentials |
| `~/.gitconfig`, `~/.arcrc` | ro | VCS config |
| `~/.moz-phab-config` | rw | moz-phab config |
| `~/.nvm`, `~/.local/bin` | ro | Node, local tools |
| `~/.rustup` | ro | rust toolchains (use only) |
| `~/.cargo/bin` | ro | host-installed cargo tools |
| `~/.ssh/{known_hosts,config}` | ro | ssh known hosts / config (keys NOT exposed) |
| `$SSH_AUTH_SOCK` (if set) | rw | ssh-agent socket forwarded for signing |
| `~/src` (or `$CCODE_SRC`, or `$PWD` with `CCODE_CWD_ONLY=1`) | rw | Firefox source tree |
| `~/.claude`, `~/.claude.json` | rw | Claude state (shared with host — see residual risks) |
| `~/Library/Keychains` | rw | macOS keychain (claude OAuth token + refresh on /login) |
| `~/.mozbuild` | rw | mach build artifacts |
| `~/.sandbox/{cargo,uv,npm,npm-prefix,pip,go}` | rw | sandbox-only language toolchain caches |

There are no bind mounts on macOS, so toolchain caches are redirected via
env vars (`CARGO_HOME`, `NPM_CONFIG_CACHE`/`PREFIX`, `PIP_CACHE_DIR`,
`GOPATH`, `GOMODCACHE`, `UV_CACHE_DIR`) instead of being mounted at the
canonical paths.

The sandbox profile also `(deny mach-lookup …)`s a hand-picked list of
user-facing Mach services — pasteboard, Dock, SystemUIServer, Notification
Center, AppleEvents — so a compromised agent can't read the system
clipboard, manipulate the Dock, send notifications, or (in theory) script
other apps via AppleEvents. Note that on modern macOS, AppleEvent
delivery is gated by TCC rather than mach-lookup, so the AppleEvents deny
is best-effort; rely on TCC consent (System Settings → Privacy & Security
→ Automation) as the real defence.

Exfiltration via Claude Code's `WebFetch` / `WebSearch` tools (which
tunnel through the Anthropic API and so bypass any host-side network
filter) is mitigated by the `CCODE_NETPOLICY` netproxy — the proxy is
the single chokepoint for all egress, and Anthropic is on the allowlist
(or not) at the network level.

### Environment forwarding

The following environment variables are forwarded into the sandbox when
present: `GH_TOKEN` (read at launch via `gh auth token`),
`PHABRICATOR_TOKEN`, `BMO_API_KEY`, and `SSH_AUTH_SOCK`. Everything else
from the host environment is dropped (`--clearenv` on Linux, `env -i` on
macOS).

On macOS, Claude Code uses the login keychain as its sole credential
store, so `~/Library/Keychains` is exposed rw to the sandbox: the
in-sandbox claude reads the "Claude Code" entry on startup and rewrites
it on `/login` (token refresh). Without RW, `/login` fails with "Failed
to save API key to macOS Keychain". File-level RW does not bypass
securityd ACLs — accessing unrelated entries (Slack, 1Password, etc.)
still triggers a consent prompt or outright denial. See residual risks.

We do **not** also forward `CLAUDE_CODE_OAUTH_TOKEN` via env: doing so
in combination with a keychain-managed login key makes claude warn
about an auth conflict.

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
  them up, *outside* the sandbox. Isolating via `CLAUDE_CONFIG_DIR`
  was tried and removed: Claude Code on macOS spreads login state
  across `~/.claude/`, `~/.claude.json`, and the keychain, and isolating
  any one of them broke login UX even with a token forwarded via env
  var. The acceptable mitigation is the netproxy: a poisoned hook can
  only reach hosts on the netpolicy allowlist.

- **rustup `~/.rustup` is shared read-only.** A compromised agent cannot
  modify the host toolchain, but cannot install new toolchains either —
  `rustup install/update` must run on the host.

- **`cargo install` no longer reaches the host.** Cargo-installed CLI tools
  live in the sandbox's `~/.cargo/bin` (under `~/.sandbox/cargo/bin`). If
  you want a tool inside the sandbox, install it from inside `ccode`.

- **macOS: no PID isolation.** `bwrap` uses a PID namespace so the agent
  cannot see or signal host processes. macOS has no equivalent primitive;
  `sandbox-exec` only restricts the `signal` operation. The agent can still
  enumerate host PIDs via `ps`/`sysctl`, though it cannot send signals to
  them or read their per-process info beyond what `sysctl` exposes.

- **macOS: `sandbox-exec` is deprecated.** Apple's own man page says so. It
  remains the only unprivileged sandboxing primitive available on macOS and
  is still enforced by the kernel, but Apple may break or remove it in
  future releases. Treat the macOS sandbox as best-effort.

- **macOS: Mach IPC is mostly allowed.** `mach-lookup` is allowed broadly
  because denying it breaks system frameworks at startup. The profile
  blocks a hand-picked list of user-facing services (pasteboard, Dock,
  SystemUIServer, Notification Center, AppleEvents) but the deny list is
  not exhaustive — a compromised agent can still talk to anything else
  reachable via Mach. AppleEvents in particular is gated by TCC rather
  than sandbox-exec on modern macOS; the deny rule is a tripwire, not a
  guarantee.

- **macOS network filter is host-name granularity.** With
  `CCODE_NETPOLICY` set, the proxy filters at the TLS SNI / HTTP Host
  header (which is what almost everything checks against the allowlist).
  It does not terminate TLS, so:
   - **Domain fronting** through an allowlisted CDN-shared origin is
     possible. If you allow `*.cloudflare.com`, traffic could hit any
     other Cloudflare-fronted service. Allowlisting specific apex
     domains rather than wildcarded CDNs mitigates.
   - **DNS over HTTPS** to an allowlisted resolver bypasses the
     intended target check — the resolver host is the only host we
     filter, the content of the DoH request is not inspected.
   - **No content filtering**: a process that's allowed to talk to
     `api.anthropic.com` can do anything that endpoint accepts.
   - **WebFetch / WebSearch** tunnel through the Anthropic API, so the
     netproxy only sees them if it sees Anthropic. Either keep them out
     of the policy allowlist, or add a project-level `.claude/
     settings.json` deny manually.
  These are inherent to a non-MITM proxy. Adding MITM mode for
  per-method/per-path filtering is a planned future iteration.
