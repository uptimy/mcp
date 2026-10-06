// Command mcp serves Uptimy's remote MCP server (mcp.upti.my): a stateless
// gateway that exposes monitoring, incidents and maintenance as MCP tools on
// top of the services' existing API-key routes.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/uptimy/upti.my-mcp/internal/server"
	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

// version is set at build time with -ldflags "-X main.version=<sha>".
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	up := upstream.New(
		env("API_BASE_URL", "https://api.upti.my"),
		env("WORKFLOWS_URL", "https://workflows.upti.my"),
		env("HEARTBEATS_URL", "https://heartbeats.upti.my"),
	)
	srv := &http.Server{
		Addr: ":" + env("HTTP_PORT", "8080"),
		Handler: server.New(up, server.Config{
			PublicURL:           env("PUBLIC_URL", "https://mcp.upti.my"),
			AuthorizationServer: env("AUTHORIZATION_SERVER", "https://api.upti.my"),
			PublicHeartbeatsURL: env("PUBLIC_HEARTBEATS_URL", "https://heartbeats.upti.my"),
			Version:             version,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("listening", "addr", srv.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()

	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		slog.Error("shutdown", "err", err)
	}
}
