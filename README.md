<p align="center">
  <a href="https://www.upti.my/?utm_source=github&utm_medium=readme&utm_campaign=mcp">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://www.upti.my/brand/uptimy-logo-white.svg">
      <img src="https://www.upti.my/brand/uptimy-logo.svg" alt="Uptimy" height="48">
    </picture>
  </a>
</p>

<h3 align="center">Uptimy MCP server</h3>

<p align="center">
  Your uptime monitoring, in your AI assistant.<br>
  Ask what's down, run incidents and status page updates, and set up monitors, alerts and reports from Claude, ChatGPT or Cursor.
</p>

<p align="center">
  <a href="https://github.com/uptimy/mcp/actions/workflows/ci.yml"><img src="https://github.com/uptimy/mcp/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/uptimy/mcp" alt="License"></a>
</p>

<p align="center">
  <a href="#connect">Connect</a> ·
  <a href="#tools">Tools</a> ·
  <a href="https://www.upti.my/docs/mcp">Docs</a>
</p>

---

[Uptimy](https://www.upti.my) monitors websites, APIs and cron jobs from several regions, opens incidents when something fails, and runs status pages and uptime reports. This is its remote MCP server, hosted at `https://mcp.upti.my/mcp`. Connect it and ask:

- *Is anything down right now?*
- *Set up monitoring for my domain and alert #ops in Slack when something goes down.*
- *Post an update on the open incident: the fix is deployed and we're monitoring.*
- *How has my API's latency looked this week, per region?*
- *Did we meet our SLA last month? Add a monthly report for Acme's monitors and email it to their CTO.*

The assistant answers from your real checks, including the errors from failed ones. It also sees the third-party services you follow (GitHub, Cloudflare, Stripe, ...), so it can tell your problem apart from a provider's.

## Connect

You need an [Uptimy account](https://app.upti.my); the free plan works. You sign in with it when you connect, so there's no key to copy.

**Claude:** open **Customize → Connectors**, choose **+ Add → Add custom connector** and enter `https://mcp.upti.my/mcp`.

**ChatGPT:** add a custom connector in ChatGPT's connector settings with the same URL.

**Claude Code:**

```bash
claude mcp add --transport http uptimy https://mcp.upti.my/mcp
```

Then run `/mcp`, pick **uptimy** and choose **Authenticate**.

**Cursor** (`~/.cursor/mcp.json`) and other clients that take a URL:

```json
{
  "mcpServers": {
    "uptimy": {
      "url": "https://mcp.upti.my/mcp"
    }
  }
}
```

When you sign in, you pick the workspace the assistant may see. Each connection gets its own API key, valid for 90 days and marked **AI assistant** under **Settings → API Keys**, where you can revoke it at any time. For clients without sign-in support, create a key there and send it as a header: `"headers": { "Authorization": "Bearer upt_..." }`.

## Tools

24 tools: 10 that read and 14 that change something.

| Read | What it does |
|---|---|
| `get_status_overview` | Every monitor, cron job monitor, open incident, status page and followed third-party service, problems first |
| `get_monitor` | One monitor's target, schedule, regions and current status |
| `get_monitor_metrics` | Uptime and latency over a time range, per region, with the latest failed checks and their errors |
| `list_incidents`, `get_incident` | Recent incidents, and one incident with its timeline |
| `get_dependencies` | The third-party services Uptimy measures, their status, and which ones you follow |
| `list_reports`, `get_uptime_report` | Your uptime reports, and availability against the SLA target for any period |
| `list_alerts` | Who hears about incidents: every alert, where it sends and whether it's paused |
| `suggest_monitors` | Scans a domain you name for what's worth monitoring (the scan the app's Setup runs); adds nothing |

| Change | What it does |
|---|---|
| `create_incident`, `post_incident_update` | Open an incident; post timeline updates, change its status and publish it to status pages |
| `schedule_maintenance` | Schedule a maintenance window, optionally announced on status pages |
| `set_monitor_paused`, `trigger_check` | Pause or resume a monitor; run a check right away to confirm a fix |
| `follow_dependency` | Follow or unfollow a third-party service, so its outages open incidents in your workspace |
| `add_monitors`, `add_cron_monitor` | Add website, API, certificate, port, ping, DNS and MCP server checks; watch a cron job with a heartbeat |
| `create_status_page`, `add_to_status_page` | Create a status page (private unless you ask); show more monitors on one |
| `add_alert`, `set_alert_paused` | Send incidents to Slack, Discord, Teams, Telegram, PagerDuty, email or a webhook; pause or resume an alert |
| `add_report`, `update_report` | Add an uptime report, for example one per client emailed every month; change one |

What each tool can do depends on your plan, as in the app: reports other than the weekly default, for example, need Growth and above.

### What it won't do

- **Nothing deletes.** Removing monitors, alerts, status pages or reports stays in the [Uptimy app](https://app.upti.my) and [uptimyctl](https://github.com/uptimy/uptimyctl), and so does editing a monitor's settings or a status page's design.
- **Adding twice is safe.** The setup tools skip what's already monitored and return what already exists under the same name.
- **It asks first.** Every tool is marked as read-only or not and destructive or not, so your client can ask before running it. The server also tells the assistant to confirm with you before posting public updates, publishing a status page or pausing monitors.

## How it works

The server is a stateless gateway with no database and no credentials of its own. Every request carries the caller's API key. The server checks the key with the Uptimy API and forwards the request to the same API [uptimyctl](https://github.com/uptimy/uptimyctl) uses. Successful checks are cached for 3 minutes, so a revoked key stops working within a few minutes. Responses are trimmed before they reach the model. Monitor settings that can hold secrets (headers, bodies, tokens, passwords, scripts, ...) come back as `[redacted]`.

It logs one line per request and one per tool call: the tool, the key's ID, how long it took and any error. It never logs arguments or results.

Sign-in is OAuth 2.1 with PKCE and dynamic client registration. `https://api.upti.my` is the authorization server, found through `https://mcp.upti.my/.well-known/oauth-protected-resource/mcp`.

## Help & support

- **Questions and setup help:** ask in [Discord](https://www.upti.my/discord), in the **#help** forum. Say which assistant you connected (Claude, ChatGPT, Cursor, ...).
- **Bugs and feature requests:** [open an issue](https://github.com/uptimy/mcp/issues/new/choose).
- **Security issues:** report them privately, see [SECURITY.md](SECURITY.md).
- **Uptimy account or billing:** [support@upti.my](mailto:support@upti.my).

## Contributing

Bug reports, fixes and new tools are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, how the code is laid out and how to add a tool. Report security issues privately ([SECURITY.md](SECURITY.md)).

## License

[Apache-2.0](LICENSE)
