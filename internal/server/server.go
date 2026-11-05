// Package server wires the MCP endpoint: bearer-key auth (a pasted upt_ key or
// one obtained through OAuth) in front of a stateless Streamable HTTP handler,
// so any replica can answer any request.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/uptimy/upti.my-mcp/internal/tools"
	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

const instructions = `Uptimy monitors websites, APIs and cron jobs from several regions and runs incidents and status pages.
Start with get_status_overview: it lists every monitor, heartbeat, open incident and status page with the UUIDs the other tools take.
To set monitoring up: suggest_monitors for the user's domain, then add_monitors with what they pick; add_cron_monitor for scheduled jobs; create_status_page to show them.
Write tools change production state (public status pages, alerting); confirm with the user before posting public updates, publishing a status page or pausing monitors.`

// Config is what the server needs beyond its upstream client.
type Config struct {
	// PublicURL is where clients reach this server, e.g. https://mcp.upti.my.
	PublicURL string
	// AuthorizationServer issues the OAuth tokens (upt_ keys) this server
	// accepts, e.g. https://api.upti.my.
	AuthorizationServer string
	// PingBaseURL is the public base of heartbeat ping URLs.
	PingBaseURL string
	Version     string
	// OpenAIAppsChallenge is the domain-verification token from OpenAI's
	// plugin dashboard, served at /.well-known/openai-apps-challenge. Empty
	// until a ChatGPT listing is being verified.
	OpenAIAppsChallenge string
}

// New returns the HTTP handler for the whole service.
func New(up *upstream.Client, cfg Config) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "uptimy", Title: "Uptimy", Version: cfg.Version}, &mcp.ServerOptions{Instructions: instructions})
	tools.Register(srv, up, cfg.PingBaseURL)
	srv.AddReceivingMiddleware(logToolCalls)

	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	// OAuth discovery (RFC 9728): a 401 points the client at this server's
	// metadata, which names the API as the authorization server. The client
	// then registers, sends the user to the app's consent page and gets an
	// upt_ key, the same token a pasted key would be.
	publicURL := strings.TrimRight(cfg.PublicURL, "/")
	metadataPath := "/.well-known/oauth-protected-resource/mcp"
	metadata := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               publicURL + "/mcp",
		AuthorizationServers:   []string{strings.TrimRight(cfg.AuthorizationServer, "/")},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Uptimy",
		ResourceDocumentation:  "https://www.upti.my/docs/mcp",
	})
	requireKey := auth.RequireBearerToken(verifier(up), &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    publicURL + metadataPath,
		AllowMissingExpiration: true,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", cors(requireKey(mcpHandler)))
	mux.Handle(metadataPath, metadata)
	// Clients that don't read the 401 header look here first.
	mux.Handle("/.well-known/oauth-protected-resource", metadata)
	if token := strings.TrimSpace(cfg.OpenAIAppsChallenge); token != "" {
		mux.HandleFunc("GET /.well-known/openai-apps-challenge", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(token))
		})
	}
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return logRequests(mux)
}

// cors lets browser-based clients (e.g. MCP Inspector) call /mcp. Any origin
// is safe: requests authenticate with a bearer header, never a cookie.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
		h.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// errKeyRefused marks a key upti.my-api rejected; it becomes a 401.
var errKeyRefused = errors.New("key refused")

// verifier accepts full-scope upt_ keys. The API owns API keys, so it asks
// the API about the key instead of reading the api_key table; agent-scoped
// keys are refused here because every tool needs workspace-wide access.
// Answers are cached for 3 minutes (see keyCache).
func verifier(up *upstream.Client) auth.TokenVerifier {
	cache := newKeyCache()
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if !strings.HasPrefix(token, "upt_") {
			return nil, auth.ErrInvalidToken
		}
		key, err := cache.get(ctx, token, func(ctx context.Context) (verifiedKey, error) {
			var res struct {
				UUID      string     `json:"uuid"`
				Scope     string     `json:"scope"`
				ExpiresAt *time.Time `json:"expiresAt"`
				Workspace struct {
					Name string `json:"name"`
				} `json:"workspace"`
			}
			err := up.Do(ctx, token, upstream.API, http.MethodGet, "/v1/api/api-key", nil, nil, &res)
			var upErr *upstream.Error
			// 404: the key's workspace is no longer subscribed.
			if errors.As(err, &upErr) && (upErr.StatusCode == http.StatusUnauthorized || upErr.StatusCode == http.StatusForbidden || upErr.StatusCode == http.StatusNotFound) {
				return verifiedKey{}, errKeyRefused
			}
			if err != nil {
				return verifiedKey{}, fmt.Errorf("verify key: %w", err)
			}
			if res.Scope != "full" {
				return verifiedKey{}, errKeyRefused
			}
			return verifiedKey{uuid: res.UUID, workspace: res.Workspace.Name, expiresAt: res.ExpiresAt}, nil
		})
		if errors.Is(err, errKeyRefused) {
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			// The API couldn't answer (not a refused key): the client sees
			// a failure, so record why.
			slog.Error("key verification failed", "error", err.Error())
			return nil, err
		}
		info := &auth.TokenInfo{
			UserID: key.uuid,
			Extra:  map[string]any{tools.KeyExtra: token, tools.WorkspaceExtra: key.workspace},
		}
		if key.expiresAt != nil {
			info.Expiration = *key.expiresAt
		}
		return info, nil
	}
}
