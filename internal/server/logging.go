package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewLogger writes one JSON object per line with the same fields as every
// other Uptimy service: {"level":"info","message":"…","timestamp":"…", …}.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				return slog.String("timestamp", a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
			case slog.LevelKey:
				return slog.String("level", strings.ToLower(a.Value.String()))
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	}))
}

// statusWriter remembers the status code for the request log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush keeps streamed responses working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests writes a line per request, except health probes.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/health/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"durationMs", time.Since(start).Milliseconds(),
			"ip", r.Header.Get("CF-Connecting-IP"),
		)
	})
}

// logToolCalls writes a line per tool call: which tool, which key, how long,
// and the error a client was given. Tool errors go back to the client as
// results, so without this nothing records them.
func logToolCalls(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if !ok || method != "tools/call" {
			return next(ctx, method, req)
		}
		start := time.Now()
		result, err := next(ctx, method, req)

		attrs := []any{"tool", call.Params.Name, "durationMs", time.Since(start).Milliseconds()}
		if call.Extra != nil && call.Extra.TokenInfo != nil {
			attrs = append(attrs, "keyUuid", call.Extra.TokenInfo.UserID)
		}
		switch res, _ := result.(*mcp.CallToolResult); {
		case err != nil:
			slog.Error("tool call failed", append(attrs, "error", err.Error())...)
		case res != nil && res.IsError:
			slog.Warn("tool call returned an error", append(attrs, "error", resultText(res))...)
		default:
			slog.Info("tool call", attrs...)
		}
		return result, err
	}
}

// resultText is the first text content of a result, cut to 300 characters.
func resultText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if len(t.Text) > 300 {
				return t.Text[:300] + "…"
			}
			return t.Text
		}
	}
	return ""
}
