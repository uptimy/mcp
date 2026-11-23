package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// Discovery documents, so agents and registries can find this server without
// being given its URL:
//
//   - /mcp/server-card: the MCP Server Card (SEP-2127), at the location the
//     spec recommends for a Streamable HTTP server (<endpoint>/server-card).
//     It says what the server is and where it lives, not what tools it has.
//   - /.well-known/ai-catalog.json: the domain's AI Catalog (v0.9), whose one
//     entry points at the server card.
//
// Both are public, read-only and fixed for the life of the process, so they
// are rendered once, served with open CORS, an hour of caching and an ETag.
//
// Identity matches server.json, which is what the MCP Registry lists.

const (
	serverCardPath = "/mcp/server-card"
	aiCatalogPath  = "/.well-known/ai-catalog.json"

	serverCardType = "application/mcp-server-card+json"
	aiCatalogType  = "application/ai-catalog+json"
)

func serverCard(publicURL string) map[string]any {
	return map[string]any{
		"$schema":     "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json",
		"name":        "my.upti/mcp",
		"version":     "1.0.0",
		"title":       "Uptimy",
		"description": "Uptime monitoring from your assistant: see what's down, run incidents, set up monitors and reports.",
		"websiteUrl":  "https://www.upti.my/docs/mcp",
		"repository":  map[string]any{"url": "https://github.com/uptimy/mcp", "source": "github"},
		"icons": []map[string]any{
			{"src": "https://www.upti.my/brand/uptimy-app-icon.png", "mimeType": "image/png", "sizes": []string{"1024x1024"}},
			{"src": "https://www.upti.my/brand/uptimy-app-icon.svg", "mimeType": "image/svg+xml", "sizes": []string{"any"}},
		},
		"remotes": []map[string]any{
			{"type": "streamable-http", "url": publicURL + "/mcp"},
		},
	}
}

func aiCatalog(publicURL string) map[string]any {
	return map[string]any{
		"specVersion": "0.9",
		"host": map[string]any{
			"displayName":      "Uptimy",
			"identifier":       "upti.my",
			"documentationUrl": "https://www.upti.my/docs/mcp",
			"logoUrl":          "https://www.upti.my/brand/uptimy-app-icon.svg",
		},
		"entries": []map[string]any{
			{
				"identifier":  "urn:air:upti.my:mcp",
				"type":        serverCardType,
				"url":         publicURL + serverCardPath,
				"displayName": "Uptimy",
				"description": "Uptime, cron job and API monitoring with incidents and status pages, as MCP tools.",
				"tags":        []string{"monitoring", "uptime", "incidents", "status-pages"},
				"publisher":   map[string]any{"identifier": "upti.my", "displayName": "Uptimy"},
			},
		},
	}
}

// staticJSON serves a fixed JSON document with the CORS and caching headers
// the server card spec asks for (the AI Catalog uses the same).
func staticJSON(contentType string, doc any) http.Handler {
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic(err) // the documents are literals; this can't fail at runtime
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET")
		h.Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
		h.Set("Access-Control-Expose-Headers", "ETag")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet, http.MethodHead:
		default:
			h.Set("Allow", "GET, HEAD, OPTIONS")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.Set("Cache-Control", "public, max-age=3600")
		h.Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Type", contentType)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
