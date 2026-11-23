package server_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
)

func getDoc(t *testing.T, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	return resp, doc
}

func TestServesServerCard(t *testing.T) {
	_, _, base := setup(t)
	resp, card := getDoc(t, base+"/mcp/server-card")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got HTTP %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":                "application/mcp-server-card+json",
		"Access-Control-Allow-Origin": "*",
		"Cache-Control":               "public, max-age=3600",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// The schema's required fields and limits.
	if card["$schema"] != "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json" {
		t.Errorf("$schema = %v", card["$schema"])
	}
	if name, _ := card["name"].(string); !regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`).MatchString(name) {
		t.Errorf("name = %q", name)
	}
	if d, _ := card["description"].(string); d == "" || len(d) > 100 {
		t.Errorf("description is %d chars, want 1-100", len(d))
	}
	if card["version"] == "" || card["version"] == nil {
		t.Error("no version")
	}
	remotes, _ := card["remotes"].([]any)
	if len(remotes) != 1 || remotes[0].(map[string]any)["url"] != "https://mcp.upti.my/mcp" {
		t.Errorf("remotes = %v", remotes)
	}

	// Conditional GET.
	req, _ := http.NewRequest(http.MethodGet, base+"/mcp/server-card", nil)
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Body.Close()
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: got HTTP %d, want 304", again.StatusCode)
	}
}

func TestServesAICatalogPointingAtServerCard(t *testing.T) {
	_, _, base := setup(t)
	resp, cat := getDoc(t, base+"/.well-known/ai-catalog.json")
	if got := resp.Header.Get("Content-Type"); got != "application/ai-catalog+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if cat["specVersion"] != "0.9" {
		t.Errorf("specVersion = %v", cat["specVersion"])
	}
	entries, _ := cat["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	e := entries[0].(map[string]any)
	if e["type"] != "application/mcp-server-card+json" || e["url"] != "https://mcp.upti.my/mcp/server-card" || e["identifier"] == nil {
		t.Errorf("entry = %v", e)
	}
}

func TestServerCardDoesNotNeedAKeyButMCPStillDoes(t *testing.T) {
	_, _, base := setup(t)
	resp, err := http.Post(base+"/mcp/server-card", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST server-card: got HTTP %d, want 405", resp.StatusCode)
	}
	resp, err = http.Post(base+"/mcp", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /mcp without a key: got HTTP %d, want 401", resp.StatusCode)
	}
}
