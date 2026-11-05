# upti.my-mcp 

Uptimy's remote MCP server, at `https://mcp.upti.my/mcp`. It lets AI assistants check what is down, read uptime and latency and uptime reports against SLA targets, run incidents and schedule maintenance.

It is a stateless gateway with no database or secrets of its own. Each request carries the caller's own `upt_` API key, which is forwarded to the same `/v1/api/*` routes [uptimyctl](../uptimyctl) uses. Incidents and maintenances go to workflows, heartbeats to the heartbeat service, uptime reports to the reports service (`REPORTS_URL`), and everything else to api. Responses are trimmed before they reach the model; for example, the raw healthcheck list carries every monitor's daily rollups.

## Tools

| Tool | Kind | Backed by |
|---|---|---|
| `get_status_overview` | read | api healthchecks + status pages, heartbeat monitors, workflows open incidents |
| `get_monitor` | read | api healthcheck, falling back to heartbeat monitor |
| `get_monitor_metrics` | read | api analytics rollups (minute/hour/day by range) + last hour of executions |
| `list_incidents` | read | workflows incidents |
| `get_incident` | read | workflows incident with timeline |
| `get_dependencies` | read | api public services catalog + workflows source follows |
| `list_reports` | read | reports definitions |
| `get_uptime_report` | read | reports range / month / quarter / weekly / live report, trimmed to the summary, monitors worst first, longest incidents, maintenance |
| `create_incident` | write | workflows incidents |
| `post_incident_update` | write | workflows incident updates, status change, status pages to show it on |
| `set_monitor_paused` | write | api healthcheck / heartbeat monitor update |
| `trigger_check` | write | api healthcheck trigger |
| `schedule_maintenance` | write | workflows maintenances |
| `follow_dependency` | write | workflows source follows |
| `suggest_monitors` | read (reaches the internet) | api discovery: the scan Setup runs, without a new workspace |
| `add_monitors` | write | api tags + bulk healthchecks, with Setup's defaults (every allowed region, 60s / ssl 1h, raised to the plan floor); skips targets already checked |
| `add_cron_monitor` | write | heartbeat monitors; returns an existing one with the same name |
| `create_status_page` | write | api status page (private unless asked) + a "Services" group |
| `add_to_status_page` | write | api status page groups: appends to the first group |

There are no delete tools, and the setup tools only add things. Editing monitors, alerting and status page design stays in the app and the CLI; see CONFIGURE_WITH_AGENTS_PLAN.md at the uptimy root.

## Connecting

Every request carries a full-scope `upt_` API key; Uptimy Agent keys are refused. You can get one through OAuth or paste one yourself. To paste one, create it in the app (Settings → API Keys) or with `uptimyctl auth login`.

**Claude.ai, ChatGPT and other clients with OAuth:** add a custom connector
with the URL `https://mcp.upti.my/mcp`. The client discovers sign-in on its own:
the server's 401 points to `/.well-known/oauth-protected-resource/mcp`, which
names `https://api.upti.my` as the authorization server. You approve on
app.upti.my and pick a workspace. The client then receives a 90-day `upt_` key,
listed under Settings → API Keys as "<client> (MCP)". The whole flow is
diagrammed in section 14 of ARCHITECTURE.md.

**Claude Code** with a pasted key:

```bash
claude mcp add --transport http uptimy https://mcp.upti.my/mcp \
  --header "Authorization: Bearer upt_..."
```

Leave out `--header` to sign in with OAuth instead.

**Cursor** (`~/.cursor/mcp.json`) and other clients that take a URL and headers:

```json
{
  "mcpServers": {
    "uptimy": {
      "url": "https://mcp.upti.my/mcp",
      "headers": { "Authorization": "Bearer upt_..." }
    }
  }
}
```

## Development

```bash
make test   # end-to-end tests: MCP client -> server -> fake upstream
make build
make run    # listens on :8080, talks to production by default
```

| Env var | Default |
|---|---|
| `HTTP_PORT` | `8080` |
| `API_BASE_URL` | `https://api.upti.my` |
| `WORKFLOWS_URL` | `https://workflows.upti.my` |
| `HEARTBEATS_URL` | `https://heartbeats.upti.my` |
| `PING_BASE_URL` | `https://ping.upti.my` (used in ping URLs) |
| `PUBLIC_URL` | `https://mcp.upti.my` (OAuth resource URL) |
| `AUTHORIZATION_SERVER` | `https://api.upti.my` |
| `OPENAI_APPS_CHALLENGE` | unset; ChatGPT's domain-verification token, served at `/.well-known/openai-apps-challenge` when set |

## Deployment

The service runs on the homelab cluster in `uptimy-production`, from `~/homelab/apps/uptimy/mcp/base`. Upstream calls use the in-cluster service addresses. It runs 2 replicas in MCP's stateless Streamable HTTP mode, so it needs no sticky sessions.

A push to `master` builds `ghcr.io/uptimy/upti.my-mcp:<sha>` and commits `deploy(mcp): <sha>` to homelab. Argo CD then syncs it.
