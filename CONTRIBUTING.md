# Contributing to the Uptimy MCP server

Thanks for helping. Bug reports, fixes and new tools are all welcome. This page covers how to get set up, how the code is laid out, and how to add a tool.

For anything bigger than a fix (a new tool, a change in what a tool does), please [open an issue](https://github.com/uptimy/mcp/issues/new/choose) first so we can agree on the approach before you spend time on it.

## Setting up

You need Go 1.25+.

```bash
git clone https://github.com/uptimy/mcp && cd mcp
make test
```

| Command | What it does |
| --- | --- |
| `make test` | End-to-end tests: an MCP client calls the server, which talks to a fake Uptimy API |
| `make lint` | `go vet` and [golangci-lint](https://golangci-lint.run/welcome/install/), the same as CI |
| `make build` | A binary in `bin/` |
| `make run` | The server on http://localhost:8080, talking to the production Uptimy API |

`make run` needs no setup: send your own API key (create one under **Settings → API Keys** in the [Uptimy app](https://app.upti.my)) and call it from any MCP client, for example:

```bash
claude mcp add --transport http uptimy-dev http://localhost:8080/mcp \
  --header "Authorization: Bearer upt_..."
```

Write tools change your real workspace, so try them on a workspace you don't mind changing.

| Variable | Default | |
| --- | --- | --- |
| `HTTP_PORT` | `8080` | |
| `API_BASE_URL` | `https://api.upti.my` | Keys, monitors, status pages, tags |
| `WORKFLOWS_URL` | `https://workflows.upti.my` | Incidents, maintenance, alerts |
| `HEARTBEATS_URL` | `https://heartbeats.upti.my` | Cron job monitors |
| `REPORTS_URL` | `https://reports.upti.my` | Uptime reports |
| `PING_BASE_URL` | `https://ping.upti.my` | Used in the ping URLs the tools return |
| `PUBLIC_URL` | `https://mcp.upti.my` | The OAuth resource URL |
| `AUTHORIZATION_SERVER` | `https://api.upti.my` | Named in the protected resource metadata |
| `OPENAI_APPS_CHALLENGE` | unset | ChatGPT's domain verification token, served at `/.well-known/openai-apps-challenge` |

## How the code is laid out

```
cmd/mcp/            entrypoint: reads the environment and starts the server
internal/server/    HTTP: key checks (with a 3-minute cache), OAuth metadata, the
                    server instructions, request and tool call logs; the tests
internal/tools/     the tools, one file per area (monitors, incidents, setup, alerts, reports, ...)
internal/upstream/  the client for Uptimy's API, which turns its errors into messages a model can act on
```

The server is stateless. Each request carries the caller's key, and every tool calls the same `/v1/api/*` routes [uptimyctl](https://github.com/uptimy/uptimyctl) uses, with that key. There's no database and nothing to configure beyond the table above.

## Adding a tool

**1. Register it** in `Register` in `internal/tools/tools.go`:

```go
mcp.AddTool(srv, &mcp.Tool{
	Name:        "trigger_check",
	Title:       "Run check now",
	Description: "Run a website/API monitor's check immediately from all its regions, e.g. to confirm a fix. Read the result with get_monitor_metrics a few seconds later.",
	Annotations: write(false, false, false),
}, t.triggerCheck)
```

The description is what the model reads to decide when to call the tool: say what it does, what it returns, and which tool to use next. Every tool sets all its hints, because directories (Claude, ChatGPT) review them: `readOnly()`, `readOnlyOpenWorld()` for a read that reaches outside Uptimy, or `write(destructive, idempotent, publishes)`. A write is destructive when it can turn something off, and `publishes` when it can change what a public status page shows.

**2. Write the handler** in the file for its area. Input and output are structs: their JSON tags and `jsonschema` descriptions become the tool's schema.

```go
func (t *toolset) triggerCheck(ctx context.Context, req *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, actionOut, error) {
	// ...
	if err := t.call(ctx, req, upstream.API, http.MethodPatch, "/v1/api/healthchecks/"+url.PathEscape(in.UUID)+"/trigger", nil, nil, nil); err != nil {
		return nil, actionOut{}, err
	}
	return nil, actionOut{OK: true, Message: "Check queued for ..."}, nil
}
```

- `t.call` sends the request with the caller's key; pick the service the route lives on (`upstream.API`, `Workflows`, `Heartbeats` or `Reports`).
- Return only what a model needs: names, statuses, times and UUIDs, not the raw API payload. Pass monitor settings through `redactConfig`.
- Errors are sentences the model can act on, or relay to the user ("heartbeats wait for check-ins from your job and can't be run on demand").
- A tool that adds something is safe to call twice: skip or return what already exists.
- No tool deletes anything. Deleting stays in the app and uptimyctl.

**3. Test it** in `internal/server/`: add the routes it calls to the fake upstream in `server_test.go`, then call the tool through the MCP client with `call(t, cs, "trigger_check", map[string]any{...})` and check what it sent (`fake.write("PATCH /v1/api/...")`) and returned. Update the tool count in `TestListsTools`; it also checks every tool's title and hints.

**4. Document it**: the tool tables in the README, and the server instructions in `internal/server/server.go` if the model should know when to reach for it.

## Pull requests

- Keep a PR to one change, with tests. `make lint` and `make test` should pass; CI runs both.
- Describe what changes for people using an assistant, not only the code.
- Match the code around you: comments explain *why*, errors are sentences a user can act on, and nothing logs or returns a key or a monitor's secrets.
- A merge to `master` deploys to `https://mcp.upti.my` within minutes, so `master` must always be ready to run.
- By contributing you agree your work is licensed under the [Apache 2.0 license](LICENSE).

Found a security issue? Please don't open an issue; see [SECURITY.md](SECURITY.md).
