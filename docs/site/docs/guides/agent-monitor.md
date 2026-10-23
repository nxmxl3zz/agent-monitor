---
title: Agent Monitor for Codex and Claude
description: Watch Codex and Claude quotas, per-session activity and token usage from a terminal or your phone.
---

# Agent Monitor for Codex and Claude

Agent Monitor combines the Codex and Claude subscription quotas with a
per-session activity table and a token usage dashboard. It reuses the
OpenUsage providers and accounting, and adds a dedicated terminal view, a
mobile-friendly web dashboard and private remote access through Tailscale.

## Start

From the project root:

```bash
make build
./scripts/agent-monitor service install
./scripts/agent-monitor
```

The `agent-monitor.service` user service collects data in the background and
starts with your Linux session. You can close the view and reopen it at any
time. After rebuilding, restart the service so it loads the new binary.

```bash
./scripts/agent-monitor service status
./scripts/agent-monitor service restart
./scripts/agent-monitor service stop
```

## Controls

| Key | Action |
|---|---|
| `Tab` | Switch between both providers, Codex or Claude. |
| `↑` / `↓` | Select a session to see its identifier and last tool. |
| `/` | Filter by project, model, identifier or tool; Enter confirms. |
| `Esc` | Clear the filter. |
| `h` | Change period: today, 3, 7 or 30 days, or all. |
| `t` | Cycle and save the theme. |
| `r` | Request a quota refresh. |
| `q` | Close the view; the collector keeps running. |

Quotas are checked about once a minute. The view reads local activity every
second. Tokens can take a few more seconds to appear while the collector
accounts for them.

`Working` and `Needs input` reflect observed events. After two minutes without
recent events they become `Unconfirmed`. `Turn done` means the client logged a
final answer; it does not mean the process exited. When the collector is
unreachable, data is marked as previous and active states stop being shown as
current.

Tokens belong to the selected period. `—` means no usage is available for that
session yet. Codex and Claude quota percentages are kept separate. Child
sessions are linked only when the logs provide an explicit link; they are never
inferred from timing.

## Customize

The launcher creates `.runtime/config/settings.json` from
`configs/agent-monitor.json` the first time it runs. Edit the local copy;
later runs keep it. The profile only collects Codex and Claude Code and hides
estimated API costs.

| Path, relative to the project root | Contents |
|---|---|
| `.runtime/config/` | Monitor settings and themes. |
| `.runtime/state/openusage/` | Database, socket and collector state. |

Authentication and logs are read from the official clients' usual
directories. Coverage of an invocation depends on it leaving readable local
logs, whether it is a CLI call or a session started through MCP.

When the Claude token expires, the provider tries to renew it by running
`claude auth status --json`. The official client manages its own credentials;
the monitor never sends a prompt to renew them. If the session can no longer be
renewed, sign in again with the official client.

Anthropic rate-limits the quota endpoint. The last good reading is kept in
memory, so a short throttle does not blank the gauges. Right after a collector
restart there is no cached reading yet, and the Claude card shows
`Rate limited · retrying` until the endpoint answers again.

## Web dashboard

The web view uses the same collector and runs independently of the terminal.
To keep it running on your computer:

```bash
./scripts/agent-monitor web-service install
```

Open `http://127.0.0.1:8787` on the computer to check it. You can also run it in
the foreground with `agent-monitor web`. Manage the service with
`agent-monitor web-service status`, `restart`, `stop` and `uninstall`.

The page shows a gauge for each quota (Codex and Claude, 5-hour and weekly
windows) and a **Token usage** section with four charts:

- **Total tokens by model**: the outer ring splits tokens by model and the inner
  ring by token type. Hover (or tap) the center to see the fresh input, cache
  read, cache write and output breakdown.
- **Token type breakdown** across the period.
- **Token usage by model** across the period.
- **Input vs output rate**, in tokens per minute, with one axis per line
  because output is far smaller than input.

The period (1h, 6h, 24h, 7d or 30d) and the provider and model filters live in
the top bar, which stays visible while you scroll. Tapping a model in the ring
or its legend filters by it without hiding the others; tapping it again, the
`×` in the bar or `Esc` clears the filter. Tapping the Codex or Claude quota
card filters by that provider. Filters and period are kept in the URL, so they
survive a reload and can be bookmarked.

Hovering a time chart marks the same interval on the other two. With a mouse,
drag across any of them to zoom all three, and double-click to return to the
full period. The expand button opens a chart full screen, and the section
header collapses the charts.

The page refreshes every five seconds while visible; it never queries the
providers on each refresh. Zoom and hidden series survive refreshes. When the
connection drops, it keeps the previous charts and marks the page as offline.
Web filters do not change the terminal period.

Fresh input excludes cache reads and writes. The total counts cache once. The
rate is the average per interval. Charts are computed from deduplicated events
and exclude synthetic records. Quota percentages belong to the provider account
and are not recalculated when you filter by model or period.

## Private access from your phone with Tailscale

The web server only listens on loopback and has no password of its own:
remote access is authenticated by Tailscale. There are two ways to set it up.

### Option A: dedicated rootless node (automated)

This installs the official static Tailscale build as a user service, with no
root permissions, as a separate machine named `agent-monitor` on your tailnet:

```bash
./scripts/agent-monitor remote install   # download, verify SHA-256, start services
./scripts/agent-monitor remote login     # open the printed link and approve the node
./scripts/agent-monitor remote status    # shows the URL once ready
```

The connector service watches the node, sets the allowed hosts for the web
service and configures Tailscale Serve on its own. On your phone, install the
Tailscale app, sign in to the same tailnet and open `http://agent-monitor`
(the short MagicDNS name) or the full `http://agent-monitor.<tailnet>.ts.net`.

| Command | Effect |
|---|---|
| `agent-monitor remote status` | Connection state and current URL. |
| `agent-monitor remote stop` / `start` / `restart` | Control remote access; the terminal and collector keep running. |
| `agent-monitor remote login` | Re-authenticate the node. |
| `agent-monitor remote uninstall` | Remove the services and binaries; the node identity is kept. |
| `agent-monitor tailscale …` | Run any Tailscale CLI command against this node, e.g. `agent-monitor tailscale serve status`. |

The connector first publishes HTTP inside the tailnet, whose transport is
already encrypted by WireGuard. It also tries to enable HTTPS; when Tailscale
needs an admin action, `remote status` shows `enable_https_url`. After you
complete it, the connector switches to the HTTPS address automatically.

The `tail…ts.net` suffix is assigned by Tailscale. You can swap it for one of
the readable names the admin console offers under **DNS → Rename tailnet**; the
connector picks up the new name and updates the allowed host and Serve.

Binaries live in `~/.local/lib/agent-monitor/tailscale/` and the node state in
`~/.local/state/agent-monitor-tailscale/`, readable only by your user. The
services are `agent-monitor-tailscale.service` and
`agent-monitor-remote.service`. The connector status is in
`.runtime/remote/status.json`.

### Option B: an existing Tailscale install

If the computer already runs Tailscale, look up its DNS name with
`tailscale status`, allow it on the web service and publish the dashboard with
Serve:

```bash
./scripts/agent-monitor web-service install your-pc.your-tailnet.ts.net
sudo tailscale serve --bg --https=443 http://127.0.0.1:8787
```

If port 443 is already used by Serve for another app, pick a different HTTPS
port and add `:port` to the allowed host. Open the HTTPS URL Serve prints on
your phone with Tailscale connected. No router ports are needed. See the
[Serve documentation](https://tailscale.com/docs/features/tailscale-serve).

Serve grants access to the users and devices your tailnet policy allows. Use
**Serve**, never Funnel, and never expose the server without access control.
Allowing a host does not authenticate anyone.

### What the web API exposes

The web API only publishes quotas, aggregated token usage and session
metadata. The dashboard endpoint (`/api/dashboard`) carries no sessions; only
`/api/snapshot` lists them, with project basenames instead of full paths.
Neither includes credentials, diagnostics, prompts, responses or tool
arguments.

## Snapshots and development

```bash
./scripts/agent-monitor --json
./scripts/agent-monitor export --source direct --output .runtime/snapshots.json
```

The first command prints quotas and sessions from the collector and omits raw
provider metadata. The second queries the providers directly and may include
account metadata, so keep its output private.

To run the collector in the foreground, stop the service first and run:

```bash
./scripts/agent-monitor telemetry daemon run --collect-interval 2s --poll-interval 60s
```

Then open the view from another terminal. Ctrl+C stops the foreground
collector. To uninstall the service while keeping local history:

```bash
./scripts/agent-monitor service uninstall
```
