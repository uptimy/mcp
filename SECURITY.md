# Security policy

The Uptimy MCP server handles every connected assistant's API key and can change a workspace's monitoring, alerts and public status pages, so we take reports seriously and respond quickly.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through GitHub: on the [Security tab](https://github.com/uptimy/mcp/security), choose **Report a vulnerability**. Include what you found and how to reproduce it: the client you connected with and the tool calls, with any keys removed.

You'll hear back within 3 working days. We'll keep you updated while we fix it, credit you if you'd like, and ask you to hold off on public disclosure until the fix is deployed.

## Supported versions

The server runs only as the hosted service at `https://mcp.upti.my/mcp`, deployed from `master`, so fixes reach everyone at once. There are no releases to upgrade.

## What's in scope

Anything that lets someone do more than their key allows, for example:

- reaching another workspace's data, or using a key that's been revoked, expired or isn't a full-scope workspace key
- getting the server to reveal a key, or a monitor's secrets (headers, bodies, tokens, passwords) that it should return as `[redacted]`
- a tool deleting something, or changing more than its description says
- tool descriptions or server instructions that a third party can influence (prompt injection through the server itself)
- weaknesses in the OAuth flow: the protected resource metadata here, or sign-in and consent at `api.upti.my` / `app.upti.my`

Things an assistant does with tools it was given, like posting an update the user asked for, are how the server is meant to work. Prompt injection that reaches the assistant through your own data (a monitor's name, an incident update) is worth reporting too, but it's mostly the assistant's to handle; we'll look at what the server can do to limit it.
