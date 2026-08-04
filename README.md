# moz-local-sandbox

Sandbox for running Claude Code (`claude`) against a Firefox checkout.

- **Linux:** `bwrap`-based, supports `rr` via rr-mcp. Script: `ccode`.
- **macOS:** `sandbox-exec` (Seatbelt) based. Script: `ccode-macos`.

## Usage

### Setup

If you have your source code in `~/src` and you're ok with sharing all this in
the sandbox and forwarding an SSH agent, you can proceed to the next step.
Otherwise, check the section `Env vars` below to change the default policy.

### Then

```
ccode [claude-args...]
ccode --exec PROGRAM [args...]
```

Without `--exec`, launches `claude --permission-mode bypassPermissions` in the
sandbox. With `--exec`, runs the given program instead (shell, `mach`, etc).
This can be useful to diagnosis.

`~/src` and state dirs are writable; most of the system is read-only. Network
is shared (needed for various things under and in `mach`). MCP config
(`~/.config/claude/mcp-servers.json` or `~/.claude/mcp-servers.json`) is passed
through automatically if present.

`./install.sh` symlinks the OS-appropriate script to `~/bin/ccode`, or symlink
`ccode`/`ccode-macos` onto your `$PATH` manually.

### Env vars

- `CCODE_SRC=/path` - use a different root than `~/src`.
- `CCODE_CWD_ONLY=1` - expose only `$PWD` rw instead of all of `~/src`.
- `CCODE_EXTRA_BIN_DIR=/path` - mount a host bin dir read-only, prepended to `PATH`.
- `CCODE_NOEXEC=1` (macOS) - strip the exec bit from any file that gained it
  during the session, on exit. No automatic restore (`chmod +x` on host).
- `CCODE_NO_SSH_AGENT=1` - disable SSH agent forwarding.

### Opening URLs in the host browser

`xdg-open`/`open` are shadowed inside the sandbox and forward the URL to
`bin/ccode-open-server`, running outside the sandbox, which re-validates and
opens it for real. Allowed: `bugzilla.mozilla.org`, `phabricator.services.mozilla.com`,
`localhost`/`127.0.0.1` (any port). On Linux this is a hard boundary (no other
way to reach the OS open mechanism). On macOS it's not — LaunchServices is
reachable directly (see Residual risks) — so treat it as a guardrail there,
not a security boundary.

## Host setup

### Linux

1. **AppArmor (Ubuntu/Debian only):** the stock `unpriv_bwrap` profile blocks
   `perf_event_open` across namespaces, which breaks `rr`. Install the patched
   profile:
   ```
   sudo cp apparmor/bwrap-userns-restrict /etc/apparmor.d/bwrap-userns-restrict
   sudo apparmor_parser -r /etc/apparmor.d/bwrap-userns-restrict
   ```
   Not needed on Fedora (SELinux, unconfined by default).

2. **perf_event_paranoid:**
   ```
   sudo cp sysctl/10-perf.conf /etc/sysctl.d/10-perf.conf
   sudo sysctl -p /etc/sysctl.d/10-perf.conf
   ```
   Required for `rr`; Ubuntu's default paranoia level blocks it.

3. **Disable per-repo git hooks on the host (recommended):** the sandbox can
   write `.git/hooks/` or `core.hooksPath`/`core.fsmonitor` in any repo under
   `~/src`, which the host's git would later execute as you.
   ```
   git config --global core.hooksPath ~/.git-hooks-trusted
   mkdir -p ~/.git-hooks-trusted
   ```

### macOS

No host changes needed (`sandbox-exec` ships in the base system). Disabling
per-repo git hooks (above) is still worth doing. Verify the sandbox policy:

```
./test/test-macos.sh
```

## What's exposed

Roughly: system binaries/libs read-only; VCS credentials (`gh`, `jj`, `.gitconfig`,
`.arcrc`, `.moz-phab-config`) read-only except moz-phab config; `~/src` (or
`$CCODE_SRC`/`$PWD`) read-write; Claude state (`~/.claude*`) read-write; language
toolchain caches redirected into `~/.sandbox/`; `rr` traces and `~/.mozbuild`
read-write on Linux. macOS additionally exposes `~/Library/Keychains` read-write
(Claude Code's credential store there) and denies a short list of user-facing
Mach services (Dock, Notification Center, pasteboard-adjacent, AppleEvents).
See `ccode`/`ccode-macos` source for the exact mount/rule list.

Env vars forwarded in: `GH_TOKEN`, `PHABRICATOR_TOKEN`, `BMO_API_KEY`,
`SSH_AUTH_SOCK` (if not disabled), `MOZCONFIG`, `MOZBUILD_STATE_PATH`.
Everything else is dropped.

## Residual risks

The sandbox reduces blast radius, it doesn't eliminate it:

- **Per-repo `.git/config`** in `~/src` can plant hooks/aliases the host's git
  will run later. Mitigate with `core.hooksPath` above; treat sandbox-touched
  repos as untrusted on the host.
- **Bearer tokens (gh/arc/moz-phab) are readable**, not just unmodifiable — a
  compromised agent could exfiltrate them over the network.
- **`~/.claude`/`~/.claude.json` are shared with host claude**, read-write —
  a compromised sandbox can alter memory/settings/hooks/MCP config used by
  the host's `claude` later. (Isolating via `CLAUDE_CONFIG_DIR` was tried and
  reverted — it broke macOS login.)
- **macOS has no PID isolation** — the agent can enumerate host processes
  (not signal them).
- **macOS: `sandbox-exec` is deprecated** by Apple; still kernel-enforced
  today, but not guaranteed long-term.
- **macOS: Mach IPC is mostly allowed** (breaks AppKit startup otherwise);
  only a hand-picked deny list is blocked, so this is not comprehensive.
- **macOS: LaunchServices is reachable** — required for Firefox to start, but
  means a compromised agent can launch arbitrary apps/files via `NSWorkspace`,
  a real confused-deputy escape. The URL-open allowlist is not a hard boundary
  here.
- **macOS: the clipboard is reachable** — Firefox aborts on startup if
  pasteboard access is denied, so it's allowed; a compromised agent can read/
  write it.
- **macOS: Firefox's own per-process sandboxing is disabled** — `sandbox_init()`
  can't nest, so the outer Seatbelt profile is the only confinement in effect;
  content/GPU/etc. processes run without their usual isolation.
