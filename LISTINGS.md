# Directory listings

Where the Uptimy MCP server is listed, and everything needed to submit or update a listing. Requirements were checked on 2026-10-07 from each directory's docs; items marked *unconfirmed* weren't documented anywhere we could read.

## Status

| Directory | How | Review | Status |
|---|---|---|---|
| Official MCP Registry | `mcp-publisher` + `server.json` (in this repo) | None | Ready: needs the DNS record below, then publish |
| Claude Connectors Directory | Form at claude.ai/directory/manage | Automated scan, sometimes human | Text ready; needs a reviewer test account and the privacy policy update |
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
> See what's down, read uptime and latency across regions, and run incidents and status page updates from your assistant.

**Short description** (ChatGPT: 30 characters max)
> Uptime and incident monitoring

**Registry description** (100 characters max, in `server.json`)
> Uptime monitoring and incidents: see what's down, read uptime and latency, post status page updates.

**Description** (Claude: 2,000 characters max; ChatGPT long description: 4,000)
> Uptimy monitors websites, APIs and cron jobs from several regions, opens incidents when something fails, and runs status pages. This connector brings that into your assistant.
>
> Ask what's down right now, how a service's uptime and latency looked over any time range and in each region, or why an incident opened, and the assistant answers from your real checks, including the errors from failed checks. It also sees the third-party services you follow, such as GitHub, Cloudflare or Stripe, so it can tell your problem apart from a provider's. It can also run the incident with you: post timeline updates, move it to a new status up to Resolved, publish updates to your status pages, schedule a maintenance window, pause or resume a monitor, or run a check right away to confirm a fix.
>
> You sign in with your Uptimy account and pick the workspace the assistant may see. Each connection gets its own key, valid for 90 days, which you can revoke at any time under Settings → API Keys. Tools that change things are marked as such, the server asks the assistant to confirm with you before posting anything public, and there are no tools that delete anything. Creating and editing monitors stays in the Uptimy app and CLI.

**Starter prompts** (ChatGPT: up to 3, 128 characters each)
1. Is anything down right now?
2. How has my API's latency looked this week, per region?
3. Post an update on the open incident: the fix is deployed and we're monitoring.

**URLs and contacts**

| Field | Value |
|---|---|
| Server URL | https://mcp.upti.my/mcp (Streamable HTTP) |
| Website | https://www.upti.my |
| Documentation | https://www.upti.my/docs/mcp |
| Privacy policy | https://www.upti.my/privacy-policy (needs the section below first) |
| Terms | https://www.upti.my/terms-of-service |
| Support | support@upti.my |
| Icon | https://www.upti.my/brand/uptimy-app-icon.png (1024×1024) and `.svg` |
| Developer / company | Uptimy |

**Auth, for forms that ask:** OAuth 2.1 with dynamic client registration (RFC 7591) and PKCE S256. Protected resource metadata at `https://mcp.upti.my/.well-known/oauth-protected-resource/mcp`; authorization server `https://api.upti.my` (RFC 8414 metadata, RFC 9207 `iss`). Any HTTPS redirect URI can register, plus loopback `http://localhost:<port>` and `http://127.0.0.1:<port>`, so Claude, ChatGPT and Cursor callbacks need no allowlisting. The server also accepts an API key as a Bearer header.

**Tools:** 12. Read: `get_status_overview`, `get_monitor`, `get_monitor_metrics`, `list_incidents`, `get_incident`, `get_dependencies`. Write: `create_incident`, `post_incident_update`, `schedule_maintenance` (these three can publish to public status pages, so they're marked open-world), `set_monitor_paused` and `follow_dependency` (destructive, idempotent: they can turn monitoring or a follow off), `trigger_check`. Every tool has a title and explicit read-only, destructive and open-world hints; a test in `internal/server` keeps it that way.

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

Submit at https://claude.ai/directory/manage → Submit new → MCP connector (needs a Pro, Max, Team or Enterprise plan; on Team/Enterprise, an Owner).

- **Fields:** the shared text above; reads and writes: both; slug: `uptimy`; 1–5 categories, picking the closest to monitoring and developer tools.
- **Prerequisites:** an Uptimy account (free plan works).
- **Test credentials:** a reviewer account, signed in with email and password, no 2FA, with a populated workspace: several monitors, at least one failing, an open incident and a status page. The throwaway workspace planned for the launch video can be this account.
- **Tool testing:** confirm every tool was run as a custom connector. The read tools were run from Claude Code on 2026-10-07; run the five write tools against the reviewer workspace before submitting.
- **Data handling answers:**
  - *What data does the connector access?* Monitors and their configuration (without request headers, credentials or bodies, which are never returned), check results and errors, uptime and latency metrics, incidents and their timelines, status pages, maintenance windows.
  - *Where is it stored?* Nothing is stored by the connector; it's a stateless gateway to Uptimy's API. The OAuth sign-in creates an API key in the user's workspace.
  - *What can it change?* Incidents and their updates, status page posts, maintenance windows, pausing monitors, running a check. Nothing can be deleted.
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
| + | Show me the timeline of the open incident. | Calls `list_incidents` then `get_incident`; lists updates and status changes in order. |
| + | Post a public update on the open incident: "We've identified the cause and are deploying a fix." | Shows the text and confirms first, then calls `post_incident_update` with `public: true`. |
| + | Schedule maintenance for the healthy monitor tomorrow from 22:00 to 23:00 UTC. | Calls `schedule_maintenance` with that monitor and window, after confirming. |
| − | Delete the failing monitor. | No tool deletes anything; explains it can't, and points to the Uptimy app. |
| − | Create a new monitor for https://example.com. | Explains creating monitors isn't available here; points to the app or uptimyctl. |
| − | What's the weather in Berlin? | Doesn't call any Uptimy tool. |

## Cursor

- The docs page has an **Add to Cursor** link: `cursor://anysphere.cursor-deeplink/mcp/install?name=Uptimy&config=eyJ1cmwiOiJodHRwczovL21jcC51cHRpLm15L21jcCJ9` (config is `{"url":"https://mcp.upti.my/mcp"}`, base64).
- Submit to https://cursor.directory (the form wasn't reachable during research; requirements *unconfirmed*).
- The official Cursor Marketplace is by application and requires open-source plugin repos.

## GitHub MCP Registry (github.com/mcp, VS Code's @mcp gallery)

Curated downstream of the official registry. GitHub's guidance (Oct 2025) is to email partnerships@github.com and host the code in a public repo. Whether official-registry entries now appear there automatically is *unconfirmed*. Publish to the official registry first, then email.

## Smithery

Submit https://mcp.upti.my/mcp at https://smithery.ai/new (or `smithery mcp publish "https://mcp.upti.my/mcp" -n @uptimy/uptimy`). It signs in during its tool scan; the server already answers 401 with the metadata pointer, as Smithery requires.

## Privacy policy: section to add

The current policy (last updated September 12, 2025) doesn't mention integrations or connected assistants. Directories check that it covers what the connector does with data. Proposed section, for review:

> **Connected applications and AI assistants**
>
> You can connect applications to your Uptimy workspace, such as the Uptimy CLI, Uptimy Agent, or an AI assistant through Uptimy's MCP server (for example Claude, ChatGPT or Cursor). When you approve a connection, We create an API key for it with the access shown on the approval page, and You can revoke it at any time under Settings → API Keys.
>
> A connected application can read the workspace data its key allows, such as monitors, check results, incidents and status pages, and can make the changes its key allows, such as posting incident updates. When You use an AI assistant, the data it reads from Uptimy is sent to that assistant's provider and handled under that provider's terms and privacy policy. Uptimy's MCP server does not store the data it passes on. We never return monitor request headers, credentials or request bodies to connected assistants.

## Follow-ups

- **CIMD** (Client ID Metadata Documents): Anthropic and OpenAI both prefer it to dynamic registration. Today every new connection registers a new OAuth client row; CIMD would also remove that growth.
- **Public repo:** making this repo public unlocks the Cursor Marketplace, the GitHub registry, mcp.so and MCP Market. It holds no secrets.
- **Reviewer account:** create it with the launch video's throwaway workspace, and keep it populated.
