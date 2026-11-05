package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/server"
	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

const goodKey = "upt_good"

// fakeUpstream serves the api, workflows and heartbeat routes the tools call
// (one server stands in for all three) and records every write.
type fakeUpstream struct {
	mu         sync.Mutex
	writes     map[string]map[string]any // "METHOD path" -> body
	queries    map[string]url.Values     // path -> last query
	keyLookups int
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+goodKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Invalid API key"}`)
		return
	}
	f.mu.Lock()
	if f.queries == nil {
		f.queries = map[string]url.Values{}
	}
	f.queries[r.URL.Path] = r.URL.Query()
	f.mu.Unlock()
	if r.URL.Path == "/v1/api/api-key" {
		f.mu.Lock()
		f.keyLookups++
		f.mu.Unlock()
	}
	if r.Method != http.MethodGet {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.writes[r.Method+" "+r.URL.Path] = body
		f.mu.Unlock()
	}
	data, ok := map[string]string{
		"GET /v1/api/api-key": `{"uuid":"k1","scope":"full","expiresAt":null,"workspace":{"id":1,"name":"Acme"}}`,
		"GET /v1/api/healthchecks/": `{"results":[
			{"uuid":"hc-ok","active":true,"intervalSeconds":60,"healthcheckTypeId":1,"applicationUuid":"app-ok","config":{"uri":"https://acme.test"},"healthcheckType":{"id":1,"name":"HTTP"},"application":{"uuid":"app-ok","name":"Website","status":"Healthy"},"schedulers":[{"uuid":"s-fra","name":"Frankfurt"}],"analyticsDailyRegionRollups":[{"huge":"payload"}]},
			{"uuid":"hc-down","active":true,"intervalSeconds":30,"healthcheckTypeId":1,"applicationUuid":"app-down","config":{"uri":"https://api.acme.test/health"},"healthcheckType":{"id":1,"name":"HTTP"},"application":{"uuid":"app-down","name":"Checkout API","status":"Down","lastStatusUpdate":"2026-10-06T10:00:00Z"},"schedulers":[{"uuid":"s-fra","name":"Frankfurt"}]}]}`,
		"GET /v1/api/healthchecks/hc-down": `{"uuid":"hc-down","active":true,"intervalSeconds":30,"timeoutSeconds":10,"healthcheckTypeId":1,"config":{"url":"https://acme.test/checkout","headers":{"Authorization":"Bearer secret"}},"application":{"uuid":"app-down","name":"Checkout API","status":"Down"},"schedulers":[{"uuid":"s-fra","name":"Frankfurt"},{"uuid":"s-sin","name":"Singapore"}],"tags":[{"id":7,"name":"prod"}]}`,
		"GET /v1/api/healthchecks/hc-down/analytics/minute-region": `{"results":[
			{"minuteBucket":"2026-10-06T10:00:00Z","schedulerUuid":"s-fra","totalCount":"2","successCount":"1","failureCount":"1","latencySumUs":"400000","latencyMaxUs":"300000","latencyP95Us":"300000"},
			{"minuteBucket":"2026-10-06T10:00:00Z","schedulerUuid":"s-sin","totalCount":"2","successCount":"2","failureCount":"0","latencySumUs":"200000","latencyMaxUs":"120000","latencyP95Us":"110000"}]}`,
		"GET /v1/api/healthchecks/hc-down/analytics/executions": `{"results":[
			{"checkedAt":"2026-10-06T10:00:10Z","schedulerUuid":"s-fra","success":false,"responseTimeMs":300,"errorMessage":"HTTP 503"},
			{"checkedAt":"2026-10-06T10:00:40Z","schedulerUuid":"s-fra","success":true,"responseTimeMs":100}]}`,
		"GET /v1/api/heartbeat-monitors/":        `{"results":[{"uuid":"hb-1","name":"Nightly backup","intervalSeconds":86400,"statusSnapshot":{"status":"Down","lastPingAt":"2026-10-05T01:00:00Z"}}]}`,
		"GET /v1/api/heartbeat-monitors/hb-1":    `{"uuid":"hb-1","name":"Nightly backup","intervalSeconds":86400,"graceSeconds":600,"credential":{"publicId":"pub123"},"steps":[],"statusSnapshot":{"status":"Down"}}`,
		"GET /v1/api/heartbeat-monitors/hb-cron": `{"uuid":"hb-cron","name":"Weekday report","intervalSeconds":0,"cron":"0 9 * * 1-5","timezone":"Europe/Berlin","graceSeconds":600,"credential":{"publicId":"pub456"},"steps":[],"statusSnapshot":{"status":"Healthy"}}`,
		"GET /v1/api/incidents/":                 `{"results":[{"uuid":"inc-1","title":"Checkout API is down","severity":"high","status":"Created","startedAt":"2026-10-06T10:00:00Z","incidentStatusPages":[{"statusPageUuid":"sp-1"}]}],"paginator":{"total":1}}`,
		"GET /v1/api/incidents/inc-1":            `{"uuid":"inc-1","title":"Checkout API is down","description":"Checkout failing","severity":"high","status":"Investigating","startedAt":"2026-10-06T10:00:00Z","incidentUpdates":[{"message":"Looking into it","public":true,"createdAt":"2026-10-06T10:05:00Z","user":{"firstName":"Ada","lastName":"L"}}],"statusHistory":[{"fromStatus":"Created","toStatus":"Investigating","changedAt":"2026-10-06T10:05:00Z"}],"incidentStatusPages":[{"statusPageUuid":"sp-1"}]}`,
		"GET /v1/api/status-pages/":              `{"results":[{"uuid":"sp-1","name":"Acme Status","slug":"acme"}]}`,
		"POST /v1/api/incidents/inc-1/updates/":  `{}`,
		"PUT /v1/api/incidents/inc-1":            `{}`,
		"PUT /v1/api/healthchecks/hc-down":       `{}`,
		"PUT /v1/api/heartbeat-monitors/hb-1":    `{}`,
		"PUT /v1/api/heartbeat-monitors/hb-cron": `{}`,
		"POST /v1/api/maintenances/":             `{"uuid":"m-1","title":"DB upgrade","status":"Scheduled","startAt":"2026-10-06T22:00:00Z","finishAt":"2026-10-06T23:00:00Z"}`,
		"GET /v1/public/services": `{"services":[
			{"slug":"github","name":"GitHub","workspaceId":369,"status":"degraded","probeCount":2,"uptime30":99.4},
			{"slug":"stripe","name":"Stripe","workspaceId":370,"status":"operational","probeCount":3,"uptime30":99.99}]}`,
		"GET /v1/public/services/github": `{"slug":"github","name":"GitHub","workspaceId":369,"status":"degraded","probes":[
			{"key":"website","label":"Website","meaning":"github.com is unreachable","applicationUuid":"p-web","status":"operational","uptime":{"d1":100,"d7":99.9,"d30":99.8}},
			{"key":"api","label":"API","meaning":"api.github.com errors","applicationUuid":"p-api","status":"degraded","uptime":{"d1":97.1,"d7":99.2,"d30":99.5}}]}`,
		"GET /v1/api/reports/definitions": `{"results":[
			{"uuid":"def-all","name":"All monitors","isDefault":true,"scopeType":"workspace","monitorUuids":null,"slaTarget":99.9,"cadence":"weekly","sendDay":1,"sendHour":9,"sendMinute":0,"sendTimezone":"UTC","emailEnabled":true,"recipients":["ops@acme.test"]},
			{"uuid":"00000000-0000-0000-0000-00000000acme","name":"Acme","isDefault":false,"scopeType":"monitors","monitorUuids":["app-down"],"slaTarget":99.5,"cadence":"monthly","sendDay":1,"sendHour":9,"sendMinute":0,"sendTimezone":"Europe/Sarajevo","emailEnabled":true,"recipients":["cto@acme.test","me@agency.test"]}]}`,
		"GET /v1/api/reports/periods":           `{"timezone":"UTC","weeks":[{"periodStart":"2026-10-05"}]}`,
		"GET /v1/api/reports/range":             reportJSON,
		"GET /v1/api/reports/weekly/2026-10-05": reportJSON,
		"GET /v1/api/source-follows/":           `{"results":[{"sourceWorkspaceId":369,"sourceUuid":"p-api"}]}`,
		"PUT /v1/api/source-follows/":           `{"results":[]}`,
		"DELETE /v1/api/source-follows/369":     `{}`,
		// Setting up (setup.go)
		"POST /v1/api/discovery/": `{"domain":"acme.test","quota":{"limit":10,"used":2,"remaining":8},"suggestions":[
			{"host":"acme.test","url":"https://acme.test","kind":"http","meta":{"statusCode":200,"healthy":true}},
			{"host":"api.acme.test","url":"https://api.acme.test","kind":"http","meta":{"statusCode":404,"healthy":false}},
			{"host":"acme.test","url":"https://acme.test","kind":"ssl","meta":{"certExpiresAt":"2027-01-01T00:00:00Z"}}]}`,
		"GET /v1/api/quota":       `{"healthcheckFrequency":30,"healthcheckLimit":10}`,
		"GET /v1/api/schedulers/": `{"results":[{"uuid":"s-fra","name":"Frankfurt","premium":false},{"uuid":"s-sin","name":"Singapore","premium":false}]}`,
		"POST /v1/api/tags/":      `{"id":7,"name":"prod"}`,
		"POST /v1/api/healthchecks/bulk": `{"results":[
			{"uuid":"app-new1","name":"api.acme.test","healthcheck":{"uuid":"hc-new1","intervalSeconds":60}},
			{"uuid":"app-new2","name":"acme.test SSL","healthcheck":{"uuid":"hc-new2","intervalSeconds":3600}}]}`,
		"PUT /v1/api/applications/app-new1/healthchecks/tags/": `{}`,
		"PUT /v1/api/applications/app-new2/healthchecks/tags/": `{}`,
		"POST /v1/api/heartbeat-monitors/":                     `{"uuid":"hb-new","name":"Queue worker","intervalSeconds":300,"credential":{"publicId":"pubnew"}}`,
		"POST /v1/api/status-pages/":                           `{"uuid":"sp-new","name":"Acme Public","slug":"acme-public","public":false}`,
		"POST /v1/api/status-pages/sp-new/groups/":             `{}`,
		"GET /v1/api/status-pages/sp-1":                        `{"uuid":"sp-1","name":"Acme Status","slug":"acme","public":true}`,
		"GET /v1/api/status-pages/sp-1/groups/": `{"results":[{"uuid":"g-1","label":"Services","hidden":false,"order":0,"entries":[
			{"uuid":"e-1","kind":"service","applicationUuid":"app-ok","heartbeatMonitorUuid":null,"order":0,"showDailies":true,"isVisible":true,"customLabel":"Website"}]}]}`,
		"PUT /v1/api/status-pages/sp-1/groups/g-1": `{}`,
		// Alerting (alerts.go)
		"GET /v1/api/integrations/": `{"results":[{"uuid":"int-slack","provider":"slack","name":"Acme","config":{"channelName":"ops"}},{"uuid":"int-gh","provider":"github","name":"acme/app","config":{}}]}`,
		"GET /v1/api/tags/":         `{"results":[{"id":7,"name":"prod"},{"id":8,"name":"staging"}]}`,
		"GET /v1/api/workflows/": `{"results":[
			{"uuid":"wf-default","name":"Default email alerts","enabled":true,"triggerTypes":["incident.created"],"destinations":[{"type":"email"}],"alert":null},
			{"uuid":"wf-ops","name":"Prod to #ops","enabled":false,"triggerTypes":["incident.created","incident.resolved"],"destinations":[{"type":"slack","integrationUuid":"int-slack"}],
			 "alert":{"destination":{"type":"integration","integrationUuid":"int-slack","provider":"slack"},"events":["opened","resolved"],"tagIds":[7],"severities":[],"sourceUuids":["app-down"]}}]}`,
		"POST /v1/api/alerts/": `{"uuid":"wf-new","name":"Critical to #ops","enabled":true,"triggerTypes":["incident.created","incident.resolved"],"destinations":[{"type":"slack","integrationUuid":"int-slack"}],
			"alert":{"destination":{"type":"integration","integrationUuid":"int-slack","provider":"slack"},"tagIds":[7],"severities":["critical"],"sourceUuids":["app-down"]}}`,
		"PATCH /v1/api/workflows/wf-ops": `{"uuid":"wf-ops","name":"Prod to #ops","enabled":true}`,
	}[r.Method+" "+r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Not found"}`)
		return
	}
	_, _ = io.WriteString(w, `{"data":`+data+`}`)
}

const reportJSON = `{"live":true,"methodology":"Availability is the share of monitored time a service wasn't down.","report":{
	"period":{"type":"custom","start":"2026-09-15","end":"2026-10-15","timezone":"UTC"},"slaTarget":99.9,
	"overall":{"availability":99.8,"downtimeSeconds":5400,"slaMet":false,"trend":-0.1},
	"heartbeats":{"count":1,"missedCheckins":2,"checkinRate":93.333},
	"monitors":[
		{"uuid":"app-ok","kind":"healthcheck","name":"Website","availability":100,"downtimeSeconds":0,"p95LatencyMs":120},
		{"uuid":"app-down","kind":"healthcheck","name":"Checkout API","availability":99.6,"downtimeSeconds":5400,"p95LatencyMs":800,"slowDays":3},
		{"uuid":"hb-1","kind":"heartbeat","name":"Nightly backup","availability":100,"downtimeSeconds":0,"missedCheckins":2}],
	"incidents":{"count":2,"resolved":1,"mttrSeconds":3600,"list":[
		{"title":"Checkout API is down","monitorUuid":"app-down","startedAt":"2026-10-01T10:00:00Z","resolvedAt":"2026-10-01T11:00:00Z","errorClass":"tls","regions":["Frankfurt","Singapore"]},
		{"title":"Checkout API is down","monitorUuid":"app-down","startedAt":"2026-10-14T10:00:00Z","resolvedAt":null}]},
	"maintenance":[{"title":"DB upgrade","start":"2026-10-06T22:00:00Z","end":"2026-10-06T23:00:00Z","seconds":3600,"monitors":["Checkout API"],"addedAfterStart":false}]}}`

func (f *fakeUpstream) lastQuery(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries[path]
}

func (f *fakeUpstream) write(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[key]
}

type keyTransport struct{ key string }

func (k keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+k.key)
	return http.DefaultTransport.RoundTrip(r)
}

func setup(t *testing.T) (*mcp.ClientSession, *fakeUpstream, string) {
	t.Helper()
	fake := &fakeUpstream{writes: map[string]map[string]any{}}
	up := httptest.NewServer(fake)
	t.Cleanup(up.Close)
	srv := httptest.NewServer(server.New(upstream.New(up.URL, up.URL, up.URL, up.URL), server.Config{
		PublicURL:           "https://mcp.upti.my",
		AuthorizationServer: "https://api.upti.my",
		PingBaseURL:         "https://ping.upti.my",
		Version:             "test",
	}))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: keyTransport{goodKey}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake, srv.URL
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned error: %v", name, res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return out
}

// callErr calls a tool that must fail and returns its error text.
func callErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("%s: want a tool error", name)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestRejectsMissingAndInvalidKeys(t *testing.T) {
	_, _, base := setup(t)
	for name, key := range map[string]string{"missing": "", "not upt_": "abc", "revoked": "upt_bad"} {
		req, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s key: got HTTP %d, want 401", name, resp.StatusCode)
		}
		want := `resource_metadata="https://mcp.upti.my/.well-known/oauth-protected-resource/mcp"`
		if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, want) {
			t.Errorf("%s key: WWW-Authenticate = %q, want it to contain %s", name, got, want)
		}
	}
}

func TestPublishesProtectedResourceMetadata(t *testing.T) {
	_, _, base := setup(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&meta)
		_ = resp.Body.Close()
		if meta.Resource != "https://mcp.upti.my/mcp" || len(meta.AuthorizationServers) != 1 || meta.AuthorizationServers[0] != "https://api.upti.my" {
			t.Errorf("%s: metadata = %+v", path, meta)
		}
	}
}

func TestListsTools(t *testing.T) {
	cs, _, _ := setup(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 22 {
		t.Errorf("got %d tools, want 22", len(res.Tools))
	}
	// Directory reviews (Claude, ChatGPT) require a title and explicit
	// read-only, destructive and open-world hints on every tool. Open-world:
	// it can change what public status pages show, or it reaches the internet.
	publishes := map[string]bool{"create_incident": true, "post_incident_update": true, "schedule_maintenance": true,
		"create_status_page": true, "add_to_status_page": true, "suggest_monitors": true}
	for _, tool := range res.Tools {
		a := tool.Annotations
		if tool.Title == "" || a == nil || a.DestructiveHint == nil || a.OpenWorldHint == nil {
			t.Errorf("%s: missing title or hints: %+v", tool.Name, a)
			continue
		}
		if *a.OpenWorldHint != publishes[tool.Name] {
			t.Errorf("%s: openWorldHint = %v, want %v", tool.Name, *a.OpenWorldHint, publishes[tool.Name])
		}
		if a.ReadOnlyHint && *a.DestructiveHint {
			t.Errorf("%s: read-only tools can't be destructive", tool.Name)
		}
	}
}

func TestStatusOverviewPutsProblemsFirst(t *testing.T) {
	cs, _, _ := setup(t)
	out := call(t, cs, "get_status_overview", nil)

	if out["workspace"] != "Acme" {
		t.Errorf("workspace = %v", out["workspace"])
	}
	monitors := out["monitors"].([]any)
	if len(monitors) != 3 {
		t.Fatalf("got %d monitors, want 3", len(monitors))
	}
	for i, want := range []string{"Down", "Down", "Healthy"} {
		if got := monitors[i].(map[string]any)["status"]; got != want {
			t.Errorf("monitor %d status = %v, want %v", i, got, want)
		}
	}
	if b, _ := json.Marshal(out); strings.Contains(string(b), "huge") {
		t.Error("raw rollups leaked into the overview")
	}
	if pages := out["statusPages"].([]any); pages[0].(map[string]any)["url"] != "https://acme.upti.my" {
		t.Errorf("status page url = %v", pages[0])
	}
}

func TestGetMonitorRedactsSecretsAndFallsBackToHeartbeat(t *testing.T) {
	cs, _, _ := setup(t)
	hc := call(t, cs, "get_monitor", map[string]any{"uuid": "hc-down"})
	target := hc["target"].(map[string]any)
	if target["url"] != "https://acme.test/checkout" || target["headers"] != "[redacted]" {
		t.Errorf("target = %v", target)
	}

	hb := call(t, cs, "get_monitor", map[string]any{"uuid": "hb-1"})
	if hb["kind"] != "heartbeat" || hb["pingUrl"] != "https://ping.upti.my/pub123" {
		t.Errorf("heartbeat = %v", hb)
	}
}

func TestMonitorMetricsSummarizesRollups(t *testing.T) {
	cs, _, _ := setup(t)
	out := call(t, cs, "get_monitor_metrics", map[string]any{"uuid": "hc-down", "from": "2026-10-06T09:00:00Z", "to": "2026-10-06T11:00:00Z"})

	if out["resolution"] != "minute" {
		t.Errorf("resolution = %v", out["resolution"])
	}
	overall := out["overall"].(map[string]any)
	if overall["checks"] != 4.0 || overall["failures"] != 1.0 || overall["uptimePercent"] != 75.0 || overall["avgLatencyMs"] != 150.0 {
		t.Errorf("overall = %v", overall)
	}
	failures := out["recentFailures"].([]any)
	if len(failures) != 1 || failures[0].(map[string]any)["region"] != "Frankfurt" {
		t.Errorf("recentFailures = %v", failures)
	}
}

func TestPostIncidentUpdateKeepsOtherFields(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "post_incident_update", map[string]any{"uuid": "inc-1", "message": "Fixed", "public": true, "status": "Resolved"})

	if u := fake.write("POST /v1/api/incidents/inc-1/updates/"); u["message"] != "Fixed" || u["public"] != true {
		t.Errorf("update body = %v", u)
	}
	put := fake.write("PUT /v1/api/incidents/inc-1")
	if put["status"] != "Resolved" || put["title"] != "Checkout API is down" || put["severity"] != "high" {
		t.Errorf("put body = %v", put)
	}
	if _, ok := put["statusPageUuids"]; ok {
		t.Error("status update must not touch the published status pages")
	}
}

func TestPostIncidentUpdateAddsStatusPages(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "post_incident_update", map[string]any{"uuid": "inc-1", "message": "We're on it", "public": true, "statusPageUuids": []string{"sp-2", "sp-1"}})

	put := fake.write("PUT /v1/api/incidents/inc-1")
	if put == nil {
		t.Fatal("status pages were not set")
	}
	pages, _ := put["statusPageUuids"].([]any)
	if len(pages) != 2 || pages[0] != "sp-1" || pages[1] != "sp-2" {
		t.Fatalf("statusPageUuids = %v, want the existing sp-1 plus the new sp-2 (workflows replaces the set)", put["statusPageUuids"])
	}
	if put["status"] != "Investigating" || put["title"] != "Checkout API is down" {
		t.Fatalf("PUT changed other fields: %v", put)
	}
	if up := fake.write("POST /v1/api/incidents/inc-1/updates/"); up["message"] != "We're on it" || up["public"] != true {
		t.Fatalf("public update not posted: %v", up)
	}
}

func TestPostIncidentUpdateWithoutPagesLeavesThemAlone(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "post_incident_update", map[string]any{"uuid": "inc-1", "status": "Monitoring", "public": false})
	if put := fake.write("PUT /v1/api/incidents/inc-1"); put == nil || put["statusPageUuids"] != nil {
		t.Fatalf("a status change must not send statusPageUuids (it would replace the set): %v", put)
	}
}

func TestListIncidentsDefaultsToAll(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "list_incidents", map[string]any{})
	if got := fake.lastQuery("/v1/api/incidents/").Get("status"); got != "All" {
		t.Fatalf("status = %q: without it workflows lists only open incidents", got)
	}
}

func TestSetMonitorPaused(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "set_monitor_paused", map[string]any{"uuid": "hc-down", "paused": true})
	hc := fake.write("PUT /v1/api/healthchecks/hc-down")
	if hc["active"] != false || hc["intervalSeconds"] != 30.0 || hc["healthcheckTypeId"] != 1.0 {
		t.Errorf("healthcheck body = %v", hc)
	}
	if cfg := hc["config"].(map[string]any); cfg["headers"] == "[redacted]" {
		t.Error("pause must send the real config back, not the redacted one")
	}

	call(t, cs, "set_monitor_paused", map[string]any{"uuid": "hb-1", "paused": true})
	if hb := fake.write("PUT /v1/api/heartbeat-monitors/hb-1"); hb["paused"] != true || hb["graceSeconds"] != 600.0 {
		t.Errorf("heartbeat body = %v", hb)
	}

	// A cron heartbeat keeps its schedule when paused.
	call(t, cs, "set_monitor_paused", map[string]any{"uuid": "hb-cron", "paused": true})
	if hb := fake.write("PUT /v1/api/heartbeat-monitors/hb-cron"); hb["cron"] != "0 9 * * 1-5" || hb["timezone"] != "Europe/Berlin" || hb["intervalSeconds"] != 0.0 {
		t.Errorf("cron heartbeat body = %v", hb)
	}
	got := call(t, cs, "get_monitor", map[string]any{"uuid": "hb-cron"})
	if got["cron"] != "0 9 * * 1-5" || got["timezone"] != "Europe/Berlin" {
		t.Errorf("get_monitor = %v: want the cron schedule", got)
	}
	if _, ok := got["intervalSeconds"]; ok {
		t.Errorf("get_monitor = %v: a cron heartbeat has no interval", got)
	}
}

func TestScheduleMaintenanceUsesApplicationUUIDs(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "schedule_maintenance", map[string]any{
		"title": "DB upgrade", "description": "Primary failover",
		"startAt": "2026-10-06T22:00:00Z", "finishAt": "2026-10-06T23:00:00Z",
		"monitorUuids": []string{"hc-down", "hb-1"},
	})
	services := fake.write("POST /v1/api/maintenances/")["affectedServices"].([]any)
	if len(services) != 2 || services[0].(map[string]any)["uuid"] != "app-down" || services[1].(map[string]any)["uuid"] != "hb-1" {
		t.Errorf("affectedServices = %v", services)
	}
}

func TestUpstreamErrorsBecomeToolErrors(t *testing.T) {
	cs, _, _ := setup(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "trigger_check", Arguments: map[string]any{"uuid": "hb-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "heartbeats") {
		t.Errorf("want a tool error explaining heartbeats can't be triggered, got %+v", res)
	}
}

func TestKeyIsVerifiedOncePerSession(t *testing.T) {
	cs, fake, _ := setup(t)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	call(t, cs, "get_status_overview", nil)
	call(t, cs, "get_monitor", map[string]any{"uuid": "hc-down"})
	call(t, cs, "list_incidents", nil)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.keyLookups != 1 {
		t.Fatalf("initialize, tools/list and 3 tool calls asked upti.my-api to verify the key %d times, want 1 (cached)", fake.keyLookups)
	}
}

func TestServesOpenAIAppsChallengeOnlyWhenSet(t *testing.T) {
	up := upstream.New("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	for token, want := range map[string]int{"": http.StatusNotFound, "tok-123": http.StatusOK} {
		srv := httptest.NewServer(server.New(up, server.Config{PublicURL: "https://mcp.upti.my", AuthorizationServer: "https://api.upti.my", OpenAIAppsChallenge: token}))
		resp, err := http.Get(srv.URL + "/.well-known/openai-apps-challenge")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != want || (want == http.StatusOK && string(body) != token) {
			t.Errorf("token %q: got %d %q", token, resp.StatusCode, body)
		}
	}
}

func TestOverviewListsFollowedDependencies(t *testing.T) {
	cs, _, _ := setup(t)
	out := call(t, cs, "get_status_overview", nil)
	deps := out["dependencies"].([]any)
	if len(deps) != 1 || deps[0].(map[string]any)["slug"] != "github" || deps[0].(map[string]any)["status"] != "degraded" {
		t.Errorf("dependencies = %v, want only the followed GitHub", deps)
	}
}

func TestGetDependencies(t *testing.T) {
	cs, _, _ := setup(t)
	list := call(t, cs, "get_dependencies", nil)["services"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["slug"] != "github" || list[0].(map[string]any)["followedProbes"] != 1.0 {
		t.Errorf("catalog = %v, want followed GitHub first", list)
	}
	if got := call(t, cs, "get_dependencies", map[string]any{"query": "STRI"})["services"].([]any); len(got) != 1 {
		t.Errorf("query filter = %v", got)
	}
	detail := call(t, cs, "get_dependencies", map[string]any{"slug": "github"})
	probes := detail["probes"].([]any)
	if len(probes) != 2 || probes[0].(map[string]any)["followed"] != false || probes[1].(map[string]any)["followed"] != true {
		t.Errorf("probes = %v", probes)
	}
}

func TestFollowDependency(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "follow_dependency", map[string]any{"slug": "github", "follow": true})
	put := fake.write("PUT /v1/api/source-follows/")
	if put["sourceWorkspaceId"] != 369.0 || len(put["sourceUuids"].([]any)) != 2 {
		t.Errorf("follow all = %v", put)
	}
	call(t, cs, "follow_dependency", map[string]any{"slug": "github", "follow": true, "probes": []string{"api"}})
	if uuids := fake.write("PUT /v1/api/source-follows/")["sourceUuids"].([]any); len(uuids) != 1 || uuids[0] != "p-api" {
		t.Errorf("follow one probe = %v", uuids)
	}
	call(t, cs, "follow_dependency", map[string]any{"slug": "github", "follow": false})
	if _, ok := fake.writes["DELETE /v1/api/source-follows/369"]; !ok {
		t.Error("unfollow should DELETE the service's follows")
	}
	res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "follow_dependency", Arguments: map[string]any{"slug": "github", "follow": true, "probes": []string{"nope"}}})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "website, api") {
		t.Errorf("unknown probe should list the real ones: %+v", res)
	}
}

func TestUptimeReport(t *testing.T) {
	cs, up, _ := setup(t)

	reports := call(t, cs, "list_reports", map[string]any{})
	list := reports["reports"].([]any)
	if len(list) != 2 || list[1].(map[string]any)["covers"] != "1 chosen monitors" || list[1].(map[string]any)["schedule"] != "monthly, sent day 1 at 09:00 Europe/Sarajevo" {
		t.Fatalf("list_reports: %+v", list)
	}

	out := call(t, cs, "get_uptime_report", map[string]any{})
	q := up.lastQuery("/v1/api/reports/range")
	today := time.Now().UTC().Format("2006-01-02")
	if q.Get("to") != today || q.Get("from") != time.Now().UTC().AddDate(0, 0, -29).Format("2006-01-02") || q.Get("definition") != "" {
		t.Fatalf("default is the last 30 days of the default report, got %v", q)
	}
	if out["availabilityPercent"] != 99.8 || out["slaMet"] != false || out["downtimeMinutes"] != 90.0 || out["inProgress"] != true {
		t.Fatalf("summary: %+v", out)
	}
	monitors := out["monitors"].([]any)
	if len(monitors) != 2 || monitors[0].(map[string]any)["name"] != "Checkout API" || monitors[0].(map[string]any)["slowDays"] != 3.0 {
		t.Fatalf("service monitors worst first, heartbeats left out: %+v", monitors)
	}
	longest := out["longestIncidents"].([]any)
	if longest[0].(map[string]any)["durationMinutes"] != 60.0 || longest[0].(map[string]any)["cause"] != "tls" || longest[1].(map[string]any)["ongoing"] != true {
		t.Fatalf("incidents: %+v", longest)
	}
	if len(out["plannedMaintenance"].([]any)) != 1 || out["heartbeatCheckinRatePercent"] != 93.333 {
		t.Fatalf("maintenance and heartbeats: %+v", out)
	}

	// By name, for one week.
	call(t, cs, "get_uptime_report", map[string]any{"report": "acme", "period": "last_week"})
	if q := up.lastQuery("/v1/api/reports/weekly/2026-10-05"); q.Get("definition") != "00000000-0000-0000-0000-00000000acme" {
		t.Fatalf("report by name: %v", q)
	}
	if res := callErr(t, cs, "get_uptime_report", map[string]any{"report": "Globex"}); !strings.Contains(res, "no report named") {
		t.Fatalf("unknown report: %s", res)
	}
}
