# Directory listings

Where the Uptimy MCP server is listed, and everything needed to submit or update a listing. Requirements were checked on 2026-10-07 from each directory's docs, and Claude's again on 2026-11-05; items marked *unconfirmed* weren't documented anywhere we could read.

## Status

| Directory | How | Review | Status |
|---|---|---|---|
| Official MCP Registry | `mcp-publisher` + `server.json` (in this repo) | None | Ready: needs the DNS record below, then publish |
| Claude Connectors Directory | Form at claude.ai/directory/manage | Automated scan, then listed as Community; Anthropic may pick it for Verified review | Text ready and the privacy policy is live; needs the reviewer account and the write tools run once against it |
| ChatGPT (OpenAI Plugins) | Plugin ZIP at platform.openai.com/plugins | Human | Text and test cases ready; needs business verification, a test account, the domain token and the video |
| Cursor | cursor.directory (community); Marketplace is invite-only and needs an open-source repo | Manual | "Add to Cursor" link is on the docs page; submit to cursor.directory |
| GitHub MCP Registry (VS Code) | Email partnerships@github.com | Curated | Needs a decision on a public repo |
| Smithery | smithery.ai/new with the URL | Live tool scan | Ready |
| Glama | Syncs from the official registry; or Add MCP Server → Connector | Health check | After the registry publish |
| PulseMCP | Picks up the official registry | None | After the registry publish |
| mcp.so, MCP Market | Forms that ask for a GitHub repo | Queue or paid | Skip unless the repo goes public |

## Shared listing text

**Name:** Uptimy

**One-liner** (Claude: 200 characters max)
> See what's down, read uptime and latency across regions, run incidents and status page updates, and set up monitoring, alerts and reports from your assistant.

**Short description** (ChatGPT: 30 characters max)
> Uptime and incident monitoring

**Registry description** (100 characters max, in `server.json`)
> Uptime monitoring from your assistant: see what's down, run incidents, set up monitors and reports.

**Description** (Claude: 2,000 characters max; ChatGPT long description: 4,000)
> Uptimy monitors websites, APIs and cron jobs from several regions, opens incidents when something fails, and runs status pages and uptime reports. This connector brings that into your assistant.
>
> Ask what's down right now, how a service's uptime and latency looked over any time range and in each region, why an incident opened, or whether you met your SLA last month, and the assistant answers from your real checks, including the errors from failed checks. It also sees the third-party services you follow, such as GitHub, Cloudflare or Stripe, so it can tell your problem apart from a provider's. It can run the incident with you: post timeline updates, move it to a new status up to Resolved, publish updates to your status pages, schedule a maintenance window, pause or resume a monitor, or run a check right away to confirm a fix.
>
> It can also set Uptimy up: scan your domain for what to watch, add monitors and cron job monitors, create status pages, send alerts to Slack, Teams, email or a webhook, and add uptime reports, for example one per client, emailed on a schedule.
>
> You sign in with your Uptimy account and pick the workspace the assistant may see. Each connection gets its own key, valid for 90 days, which you can see and revoke at any time under Settings → API Keys. Tools that change things are marked as such, the server asks the assistant to confirm with you before posting or publishing anything public, and there are no tools that delete anything: removing things stays in the Uptimy app and CLI.

**Starter prompts** (ChatGPT: up to 3, 128 characters each; Claude's use cases take all five)
1. Is anything down right now?
2. Set up monitoring for my domain and alert #ops in Slack when something goes down.
3. Post an update on the open incident: the fix is deployed and we're monitoring.
4. How has my API's latency looked this week, per region?
5. Did we meet our SLA last month? Add a monthly report for Acme's monitors and email it to their CTO.

**URLs and contacts**

| Field | Value |
|---|---|
| Server URL | https://mcp.upti.my/mcp (Streamable HTTP) |
| Website | https://www.upti.my |
| Documentation | https://www.upti.my/docs/mcp |
| Privacy policy | https://www.upti.my/privacy-policy |
| Terms | https://www.upti.my/terms-of-service |
| Support | support@upti.my |
| Icon | https://www.upti.my/brand/uptimy-app-icon.png (1024×1024) and `.svg` |
| Developer / company | Uptimy |

**Auth, for forms that ask:** OAuth 2.1 with dynamic client registration (RFC 7591) and PKCE S256. Protected resource metadata at `https://mcp.upti.my/.well-known/oauth-protected-resource/mcp`; authorization server `https://api.upti.my` (RFC 8414 metadata, RFC 9207 `iss`). Any HTTPS redirect URI can register, plus loopback `http://localhost:<port>` and `http://127.0.0.1:<port>`, so Claude, ChatGPT and Cursor callbacks need no allowlisting. The server also accepts an API key as a Bearer header.

**Tools:** 24. Read: `get_status_overview`, `get_monitor`, `get_monitor_metrics`, `list_incidents`, `get_incident`, `get_dependencies`, `list_reports`, `get_uptime_report`, `list_alerts`, `suggest_monitors` (open-world: it scans the user's domain). Write: `create_incident`, `post_incident_update`, `schedule_maintenance`, `create_status_page`, `add_to_status_page` (these five can change what public status pages show, so they're marked open-world), `set_monitor_paused`, `set_alert_paused`, `update_report` and `follow_dependency` (destructive, idempotent: they can turn monitoring, alerting, a report's emails or a follow off), `trigger_check`, `add_monitors`, `add_cron_monitor`, `add_alert` and `add_report` (additive; the add tools skip or return what already exists). Every tool has a title and explicit read-only, destructive and open-world hints; a test in `internal/server` keeps it that way.

## Official MCP Registry

The namespace is `my.upti/*`, proven with a DNS TXT record. HTTP proof can't work while `upti.my` redirects to `www` (the registry doesn't follow redirects); DNS also covers subdomains.

```bash
brew install mcp-publisher openssl@3
# 1. Create a signing key (keep the private key out of Git; a password manager is fine)
$(brew --prefix openssl@3)/bin/openssl genpkey -algorithm Ed25519 -out uptimy-registry.pem
PUB=$($(brew --prefix openssl@3)/bin/openssl pkey -in uptimy-registry.pem -pubout -outform DER | tail -c 32 | base64)
echo "v=MCPv1; k=ed25519; p=$PUB"
# 2. Add that string as a TXT record on the apex upti.my in Cloudflare DNS (not on a subdomain)
# 3. Log in and publish from this repo
PRIV=$($(brew --prefix openssl@3)/bin/openssl pkey -in uptimy-registry.pem -outform DER | tail -c 32 | xxd -p -c 64)
mcp-publisher login dns --domain upti.my --private-key "$PRIV"
mcp-publisher publish
```

Published versions are immutable: bump `version` in `server.json` for every change to the listing.

## Claude Connectors Directory

Submit at https://claude.ai/directory/manage → Submit new → MCP connector. Any paid Claude plan can submit (on Team/Enterprise, an Owner). Official pages: https://claude.com/docs/connectors/building/submission and https://claude.com/docs/connectors/building/review-criteria.

The portal's steps, in order (checked 2026-11-05):

1. **Connection:** `https://mcp.upti.my/mcp`. One URL for everyone (not "Users connect to different URLs").
2. **Tools:** synced from the server, grouped as read-only and write. The portal flags a missing title or annotation; ours has none missing.
3. **Listing:** name "Uptimy" (100 max), the one-liner (200 max), the description (2,000 max), 1–5 categories (closest to monitoring and developer tools), documentation URL, privacy policy URL, support contact, icon. Slug `uptimy`: **permanent once published**.
4. **Use cases:** the starter prompts above. Prerequisites: an Uptimy account (the free plan works). Reads and writes: both.
5. **Company:** Uptimy, https://www.upti.my, and a primary contact for review updates.
6. **Authentication:** sign-in required, OAuth. Leave "Anthropic holds the client" empty: dynamic registration covers it.
7. **Data handling:** the API is our own (first-party); no health data, no sponsored content.
   - *What data does the connector access?* Monitors and their configuration (request headers, bodies, scripts and fields holding credentials are removed by name before anything is returned), check results and errors, uptime and latency metrics, incidents and their timelines, status pages, maintenance windows, alerts and the names of connected integrations, uptime reports and their settings (including recipients' email addresses).
   - *Where is it stored?* Nothing is stored by the connector; it's a stateless gateway to Uptimy's API. It logs which tool was called, by which key, when and from which IP. The OAuth sign-in creates an API key in the user's workspace.
   - *What can it change?* Incidents and their updates, status page posts, maintenance windows, pausing monitors and alerts, following third-party services, running a check, adding monitors, cron job monitors, status pages, alerts and uptime reports, and changing a report's settings. Nothing can be deleted or edited beyond that.
8. **Test & launch:** the reviewer account's sign-in (email and password, no 2FA) and a populated workspace: several monitors, at least one failing, an open incident and a status page. Confirm every tool was run as a custom connector: all 24 were run against production on 2026-11-05 (claude.ai and Claude Code); run the write tools against the reviewer workspace once more before submitting.
9. **Compliance:** seven acknowledgments (directory guidelines, first-party API usage, financial transactions, AI media generation, prompt injection, conversation data collection, public documentation). Uptimy meets each: it calls only its own API, moves no money, generates no media, its tool descriptions only describe the tools, and it reads nothing from the conversation beyond the tool arguments. `suggest_monitors` probes the domain the user names (the same scan as the app's Setup), nothing else.

After submitting, an automated scan lists it as a **Community** connector. Anthropic may escalate it to **Verified** review, where reviewers run each tool. Status and feedback are in the portal; escalations go to mcp-review@anthropic.com.

Review risks to settle before submitting:

- **Missing privacy policy means immediate rejection.** The section below is in the landing PR; submit only after it's live.
- **`destructiveHint`:** the review criteria say `destructiveHint: true` is for "tools that modify or delete data". `post_incident_update` changes an incident's status and can publish to status pages, but is marked non-destructive (it's open-world). Marking it destructive makes Claude ask before every call, which the server instructions already request for public posts. Kept as is for the 2026-11 submission; change it if the review asks.
- Anthropic's docs note that client registration creates a new client per connection; supporting Client ID Metadata Documents (CIMD) is their recommendation for high traffic (follow-up below).

## ChatGPT (OpenAI Plugins)

Upload a plugin ZIP at https://platform.openai.com/plugins. Requires individual or business verification of the OpenAI organization, and the Owner role.

- **ZIP contents:** `plugin.json` (schema `https://agent-plugins.org/schemas/1.0.0/plugin.schema.json`) and `mcp.json` with `{"type": "streamable-http", "url": "https://mcp.upti.my/mcp"}`. Build the ZIP from the dashboard's template when submitting; the exact `plugin.json` fields beyond the ones listed here are *unconfirmed*.
- **Text:** `displayName` "Uptimy" (30 max), `shortDescription` above (30 max), `longDescription` = the description above, `developerName` "Uptimy", the three starter prompts.
- **URLs:** website, support (support@upti.my or a support page), privacy policy, terms. All four are required and must be HTTPS.
- **Domain verification:** OpenAI shows a token; set it as `OPENAI_APPS_CHALLENGE` in the MCP ConfigMap (`~/homelab/apps/uptimy/mcp/base/configmap.yaml`) and it's served at `https://mcp.upti.my/.well-known/openai-apps-challenge`.
- **Redirect URI:** the stable `https://chatgpt.com/connector_platform_oauth_redirect` works: the authorization server advertises and sends `iss` (RFC 9207).
- **Video walkthrough URL:** required. Use the launch video (`videos/06-mcp-server`).
- **Reviewer credentials:** in the dashboard, never in the ZIP; same reviewer account as Claude's (no 2FA, sample data).
- **Test cases** (exactly 5 positive and 3 negative):

| # | Prompt | Expected |
|---|---|---|
| + | Is anything down right now? | Calls `get_status_overview`; names the failing monitor and since when. |
| + | How has the failing API's latency looked over the last 24 hours, per region? | Calls `get_monitor_metrics`; reports uptime, average and p95 latency per region, and the recent failed checks with their errors. |
| + | Set up monitoring for example.com. | Calls `suggest_monitors`, shows what it found, and after the user picks, calls `add_monitors` with those. |
| + | Post a public update on the open incident: "We've identified the cause and are deploying a fix." | Shows the text and confirms first, then calls `post_incident_update` with `public: true`. |
| + | Schedule maintenance for the healthy monitor tomorrow from 22:00 to 23:00 UTC. | Calls `schedule_maintenance` with that monitor and window, after confirming. |
| − | Delete the failing monitor. | No tool deletes anything; explains it can't, and points to the Uptimy app. |
| − | Change the failing monitor's URL to https://example.com/health. | No tool edits a monitor; explains it can't, and points to the Uptimy app or uptimyctl. |
| − | What's the weather in Berlin? | Doesn't call any Uptimy tool. |

## Cursor

- The docs page has an **Add to Cursor** link: `cursor://anysphere.cursor-deeplink/mcp/install?name=Uptimy&config=eyJ1cmwiOiJodHRwczovL21jcC51cHRpLm15L21jcCJ9` (config is `{"url":"https://mcp.upti.my/mcp"}`, base64).
- Submit to https://cursor.directory (the form wasn't reachable during research; requirements *unconfirmed*).
- The official Cursor Marketplace is by application and requires open-source plugin repos.

## GitHub MCP Registry (github.com/mcp, VS Code's @mcp gallery)

Curated downstream of the official registry. GitHub's guidance (Oct 2025) is to email partnerships@github.com and host the code in a public repo. Whether official-registry entries now appear there automatically is *unconfirmed*. Publish to the official registry first, then email.

## Smithery

Submit https://mcp.upti.my/mcp at https://smithery.ai/new (or `smithery mcp publish "https://mcp.upti.my/mcp" -n @uptimy/uptimy`). It signs in during its tool scan; the server already answers 401 with the metadata pointer, as Smithery requires.

## Privacy policy

Directories check that the policy covers what the connector does with data; Claude rejects a submission without one. The section "Connected Applications and AI Assistants" was added in upti.my-landing (branch `mcp-launch`, 2026-11-05): which connections exist, what a key can read and change, that data an assistant reads goes to its provider, that the MCP server keeps no data and only logs tool calls, and what it removes from monitor settings. It must be live at https://www.upti.my/privacy-policy before any submission.

## Launch checklist

In order. Steps marked *you* need an account, DNS or a dashboard only you can use.

1. ~~Merge and deploy~~ (done 2026-11-05: api_key.source, Connect an AI assistant in Settings → API Keys, docs, blog, privacy policy; setup and report tools since).
2. *You:* create the reviewer account (email and password, no 2FA) and populate its workspace: several monitors, one failing, an open incident, a status page.
3. Run the write tools against that workspace from a custom connector in Claude, and fix anything that fails.
4. *You:* add the registry TXT record on `upti.my`, then `mcp-publisher validate` and `mcp-publisher publish` (Official MCP Registry). Glama and PulseMCP pick it up from there.
5. *You:* submit to the Claude Connectors Directory (steps above).
6. *You:* submit to Smithery and cursor.directory.
7. Re-render the launch video (`videos/06-mcp-server`, `node source/render.mjs video`, both formats): the rendered MP4s still say "12 tools"; there are 24 since 2026-11-05 (setup, alerts and reports).
8. *You:* OpenAI: business verification, then the plugin ZIP with the domain token, test cases and the re-rendered video.
9. Announce with `videos/06-mcp-server/copy.md` (LinkedIn, X, r/mcp, YouTube) and Discord #announcements.

## Follow-ups

- **CIMD** (Client ID Metadata Documents): Anthropic and OpenAI both prefer it to dynamic registration. Today every new connection registers a new OAuth client row; CIMD would also remove that growth.
- **Public repo:** making this repo public unlocks the Cursor Marketplace, the GitHub registry, mcp.so and MCP Market. It holds no secrets.
- **Reviewer account:** create it with the launch video's throwaway workspace, and keep it populated.
