# Linux engine deployment

Release Linux amd64 supports the existing `klm start`, `stop`, `pair`, and
`revoke-clients` commands. The daemon and restart helper detach from the terminal;
control uses a same-UID Unix socket, not HTTP or a PID file. Other non-Windows
platforms remain unsupported. This is engine/API delivery only: **no frontend
server, harness installation, provider login, Linux graph containment or ai-jail**.
Ordinary managed chats now have a nonroot subreaper/pidfd lifetime implementation;
its focused native/runtime and real messaging acceptance remains pending. Graph
execution and protected MCP secret storage remain unavailable on Linux. This adds
no cgroup delegation, syscall filter, namespace or host privilege. See
`../../LINUX_ORDINARY_TURN_SUPPORT_HANDOFF.md` before publishing the candidate.

## Install privately first

The following is a **new installation template**, not an automated installer.
Inspect existing users, paths, units, listeners and ingress before applying it.
Do not replace another workload or copy Windows engine data. Do not publish a
route until protected local startup succeeds and API binding has been inspected.

1. Verify the uploaded binary's SHA-256 against the implementer's final artifact.
   Install that release (not a dev-tag build) as root-owned `/opt/klm/bin/klm`,
   mode 0755. Internal `engine/prompts/*.md` are embedded during `go build`
   (including `-trimpath`), so the binary needs no source checkout or prompts
   directory. Optional editable prompts go in `/opt/klm/bin/prompts`; an explicit
   `KLM_PROMPTS_DIR` is authoritative and must contain every required prompt.
   Empty/unreadable prompt files fail preparation, never omit the guidance.
   Embedding covers prompts only: the general agent's internal skill still needs
   `engine/skills` packaged as `/opt/klm/bin/skills`, or an absolute `KLM_SKILLS_DIR`.
   No Go/Node installation is needed for the engine itself on the VPS.
2. If absent, create a dedicated system account:
   `useradd --system --user-group --home-dir /var/lib/klm --shell /usr/sbin/nologin klm`.
   Inspect an existing `klm` account for compatibility instead of changing it blindly.
3. Create `/var/lib/klm`, `/var/lib/klm/.config`, `/var/lib/klm/.config/klm`, and
   `/var/lib/klm/.config/klm/engine`, owned by `klm:klm`, mode 0700. Ensure parent
   paths are trusted, root/runtime-owned and not writable by unrelated users; do
   not install through symlinked or shared writable parents. Existing insecure
   data directories are refused, not silently chmodded. The final data directory
   must not be a symlink. `control.sock` must fit the Linux 107-byte path limit.
4. Install `klm.service` to `/etc/systemd/system/klm.service` (root:root, 0644).
   Create `/etc/klm` (root:root, 0700), copy and adapt `klm.env.example` to
   `/etc/klm/engine.env` (root:root, 0600). Set only the needed exact frontend
   origins and the **actual immediate TLS-terminating proxy** CIDRs. Never trust
   all networks. Keep this operational file out of source control. The unit fixes
   `HOME`, `XDG_CONFIG_HOME` and `KLM_API_BIND_HOST=127.0.0.1` before first startup.
5. Run `systemctl daemon-reload`, then `systemctl start klm`. Inspect the CLI
   startup result via `journalctl -u klm`; worker diagnostics are in the protected
   `/var/lib/klm/.config/klm/engine/engine.log`. Stop on a startup/storage error;
   do not reset/migrate a live DB by hand to work around it.
6. Before ingress, inspect `ss -ltnp '( sport = :7331 )'` as administrator: expect
   **only 127.0.0.1:7331**, not wildcard IPv4/IPv6. Inspect the actual engine UID,
   data-dir mode 0700 and socket/log modes 0600. Validate repeated CLI start with
   the same environment, then `systemctl stop klm` / `systemctl start klm` for a
   graceful lifecycle. Enable with `systemctl enable klm` only after success.

`KLM_API_BIND_HOST` accepts only unset (legacy `0.0.0.0` default), `127.0.0.1`, or
`::1`; invalid values fail before listening. The default Windows binding is
unchanged. This setting does not affect private authenticated loopback bridges.
An existing saved access policy overrides origin/proxy environment bootstrap;
use authenticated Engine settings and restart rather than deleting auth storage.

## Local administration and lifecycle

Always use systemctl for normal deployed start/stop/recovery. Run pairing and
revocation as the service user, with the service's data environment, for example:

```sh
runuser -u klm -- env HOME=/var/lib/klm XDG_CONFIG_HOME=/var/lib/klm/.config /opt/klm/bin/klm pair
runuser -u klm -- env HOME=/var/lib/klm XDG_CONFIG_HOME=/var/lib/klm/.config /opt/klm/bin/klm revoke-clients
```

`sudo klm pair` targets root's different data directory and is not recovery for
this engine. Keep one-use pairing codes ephemeral: no command arguments, logs,
traces, screenshots or documentation. Pairing does not bypass HTTPS, exact-origin,
CSRF or existing credential storage rules. Revocation reports failure if durable
auth persistence fails. Never change socket permissions to permit other users.

For a direct `start` readiness check or CLI recovery, supply **all** the unit's
HOME/XDG/bind and bootstrap origin/proxy values, not just HOME; `start` may launch
an engine if it is down. Prefer `systemctl restart klm` for recovery. `start`
waits for protected local readiness; a timed-out Linux startup terminates only
the child that invocation launched. `stop` acknowledges locally, then waits for
the data lock to release after shutdown. Only the engine-lock owner reclaims a
same-owner 0600 stale socket after connection refusal; files, symlinks, live or
ambiguous endpoints are never unlinked. Socket replacement is identity-checked
on close. Do not manually unlink a live socket or use the lock file's diagnostic
PID as authority to kill a process.

The intentionally small systemd unit is `Type=oneshot` / `RemainAfterExit=yes`,
with `Restart=no` and `KillMode=control-group`. It permits the existing detached
HTTP Restart helper to stop/start within the same cgroup. It provides boot start
and bounded group cleanup on service stop, **not automatic crash supervision**.
`active (exited)` is not proof of readiness or liveness. After a crash, use
`systemctl restart klm` and inspect startup/log output. Direct `klm stop` leaves
the oneshot unit active; reconcile it with `systemctl stop klm`. No new execution
is automatically replayed after restart. `NoNewPrivileges` is basic hardening,
not a harness sandbox or secure containment implementation.

## HTTPS ingress prerequisites

The API hostname is `https://cubiqklm.codemob.com.br`. Reuse the existing 443 router;
do not take over occupied ports, open 7331, change global firewall/TLS policy, or
restart unrelated workloads. DNS/Cloudflare credentials, certificate ownership
and actual ingress topology must be established separately by the deployer.

- TLS must be valid from browser to edge and authenticated/validated from edge to
  origin (Full strict or equivalent). No Flexible TLS or certificate-check bypass.
- The immediate trusted proxy must itself verify/terminate TLS, preserve external
  Host, remove incoming `Forwarded`/`X-Forwarded-*`, and set exactly one
  `X-Forwarded-Proto: https` based on that verified connection. Trust only its
  actual TCP peer CIDR at the engine. A generic cleartext forwarding hop must not
  assert HTTPS merely because a request supplied that header.
- A Docker proxy cannot reach host loopback as its own `127.0.0.1`. Establish a
  deliberately private TLS-aware host hop, or report a topology blocker. Do not
  switch the engine back to wildcard binding to solve container reachability.
- Preserve body bytes, methods and relevant headers; disable API caching and
  challenge pages, stream SSE without buffering with suitable idle timeouts, and
  keep upload limits compatible with engine limits. Do not log credentials,
  Cookies, Authorization or pairing bodies. Route only public API traffic, never
  private capability listeners or the control socket. Serve no UI assets here.
- Before pairing, verify DNS, TLS, the unauthenticated `/api/auth/session`
  rejection and exact-origin transport. Browser cookies remain host-only,
  Secure/HttpOnly/SameSite=Strict: localhost Chrome is cross-site with this API.
  Native Windows Tauri uses its existing native transport. Compatible same-site
  browser validation and the first-post-pairing-error stop rule are in
  `LINUX_VPS_IMPLEMENTATION_PLAN.md`; no browser fixture/frontend changes ship here.

Rollback only this hostname's route/configuration and this unit. Stop the engine,
retain protected user data and do not remove the runtime home or other sites.
Build evidence and outstanding acceptance are in
`LINUX_VPS_IMPLEMENTATION_PROGRESS.md`. No deployment is implied by these files.
