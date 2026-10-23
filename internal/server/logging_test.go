package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// captureLogs points the default logger at a buffer for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(NewLogger(&buf))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("not JSON: %v: %s", err, raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestLoggerUsesTheSharedFields(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf).Warn("hello", "tool", "get_incident")

	if !strings.Contains(buf.String(), `"level":"warn"`) || !strings.Contains(buf.String(), `"message":"hello"`) {
		t.Fatalf("fields: %s", buf.String())
	}
	line := logLines(t, &buf)[0]
	ts, _ := line["timestamp"].(string)
	if !strings.HasSuffix(ts, "Z") || len(ts) != len("2006-01-02T15:04:05.000Z") {
		t.Errorf("timestamp = %q", ts)
	}
	if _, ok := line["time"]; ok {
		t.Error("slog's time key is still there")
	}
}

func TestToolCallsAreLogged(t *testing.T) {
	buf := captureLogs(t)
	call := func(name string, result mcp.Result, err error) {
		handler := logToolCalls(func(context.Context, string, mcp.Request) (mcp.Result, error) { return result, err })
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Name: name},
			Extra:  &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "key-1"}},
		}
		_, _ = handler(context.Background(), "tools/call", req)
	}

	call("get_status_overview", &mcp.CallToolResult{}, nil)
	call("get_incident", &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "incident not found"}}}, nil)
	call("trigger_check", nil, errors.New("upstream 502"))

	lines := logLines(t, buf)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	want := []struct{ level, tool, err string }{
		{"info", "get_status_overview", ""},
		{"warn", "get_incident", "incident not found"},
		{"error", "trigger_check", "upstream 502"},
	}
	for i, w := range want {
		if lines[i]["level"] != w.level || lines[i]["tool"] != w.tool || lines[i]["keyUuid"] != "key-1" {
			t.Errorf("line %d = %v", i, lines[i])
		}
		if got, _ := lines[i]["error"].(string); got != w.err {
			t.Errorf("line %d error = %q, want %q", i, got, w.err)
		}
	}
}

func TestRequestsAreLoggedExceptHealth(t *testing.T) {
	buf := captureLogs(t)
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	for _, path := range []string{"/health/live", "/mcp"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("CF-Connecting-IP", "203.0.113.7")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	lines := logLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	if lines[0]["path"] != "/mcp" || lines[0]["status"] != float64(401) || lines[0]["ip"] != "203.0.113.7" {
		t.Errorf("line = %v", lines[0])
	}
}
